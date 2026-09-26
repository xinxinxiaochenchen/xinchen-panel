package agentproto

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

const MaxUsagePayloadBytes = 256 << 10
const MaxUsageReports = 128

// UsageReport contains only cumulative entrance-side byte counters. Identity,
// period and multiplier are resolved from the authenticated Agent and frozen
// configuration on the control plane; they are never accepted from the Agent.
type UsageReport struct {
	ConnectionID    string    `json:"connection_id"`
	LeaseID         string    `json:"lease_id"`
	Sequence        int64     `json:"sequence"`
	UploadedBytes   int64     `json:"uploaded_bytes"`
	DownloadedBytes int64     `json:"downloaded_bytes"`
	ObservedAt      time.Time `json:"observed_at"`
}

type UsageBatch struct {
	BatchID string        `json:"batch_id"`
	Reports []UsageReport `json:"reports"`
}

// UsageAck acknowledges the complete persisted batch. The digest prevents an
// ACK for a reused batch ID from deleting different unsent Agent data.
type UsageAck struct {
	BatchID string `json:"batch_id"`
	SHA256  string `json:"sha256"`
}

func DecodeUsageBatch(payload []byte) (UsageBatch, error) {
	if len(payload) > MaxUsagePayloadBytes {
		return UsageBatch{}, errors.New("usage batch exceeds 256 KiB")
	}
	var value UsageBatch
	if err := decodeStrictPayload(payload, &value); err != nil {
		return UsageBatch{}, err
	}
	if !hasExactUsageFields(payload, "batch_id", "reports") {
		return UsageBatch{}, errors.New("missing usage batch field")
	}
	if err := validateUsageBatch(&value, payload); err != nil {
		return UsageBatch{}, err
	}
	return value, nil
}

func validateUsageBatch(value *UsageBatch, payload []byte) error {
	if !uuidPattern.MatchString(value.BatchID) || len(value.Reports) == 0 || len(value.Reports) > MaxUsageReports {
		return errors.New("invalid usage batch ID or report count")
	}
	var raw struct {
		Reports []json.RawMessage `json:"reports"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil || len(raw.Reports) != len(value.Reports) {
		return errors.New("invalid usage reports")
	}
	type reportKey struct {
		connection string
		sequence   int64
	}
	seen := make(map[reportKey]struct{}, len(value.Reports))
	for i := range value.Reports {
		report := &value.Reports[i]
		if !hasExactUsageFields(raw.Reports[i], "connection_id", "lease_id", "sequence", "uploaded_bytes", "downloaded_bytes", "observed_at") ||
			!uuidPattern.MatchString(report.ConnectionID) || !uuidPattern.MatchString(report.LeaseID) || report.Sequence < 1 ||
			report.UploadedBytes < 0 || report.DownloadedBytes < 0 || report.UploadedBytes > math.MaxInt64-report.DownloadedBytes ||
			report.ObservedAt.IsZero() || report.ObservedAt.Nanosecond()%1000 != 0 {
			return errors.New("invalid usage report")
		}
		key := reportKey{strings.ToLower(report.ConnectionID), report.Sequence}
		if _, exists := seen[key]; exists {
			return errors.New("duplicate usage report")
		}
		seen[key] = struct{}{}
		report.ConnectionID = strings.ToLower(report.ConnectionID)
		report.LeaseID = strings.ToLower(report.LeaseID)
		report.ObservedAt = report.ObservedAt.UTC()
	}
	value.BatchID = strings.ToLower(value.BatchID)
	return nil
}

func UsageBatchDigest(value UsageBatch) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	decoded, err := DecodeUsageBatch(payload)
	if err != nil {
		return "", err
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func DecodeUsageAck(payload []byte) (UsageAck, error) {
	var value UsageAck
	if err := decodeStrictPayload(payload, &value); err != nil {
		return UsageAck{}, err
	}
	if !hasExactUsageFields(payload, "batch_id", "sha256") || !uuidPattern.MatchString(value.BatchID) || !digestPattern.MatchString(value.SHA256) {
		return UsageAck{}, errors.New("invalid usage acknowledgement")
	}
	value.BatchID = strings.ToLower(value.BatchID)
	return value, nil
}

// encoding/json accepts case-insensitive struct field aliases. Reject those
// aliases too so a second spelling cannot override a validated required field.
func hasExactUsageFields(payload []byte, fields ...string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(payload, &object) != nil || len(object) != len(fields) {
		return false
	}
	return hasFields(payload, fields...)
}
