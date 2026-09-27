package agentproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const MaxQuotaPayloadBytes = 4096
const MaxQuotaGrantBytes int64 = 1 << 20

// ConnectionOpen asks the control plane for the first bounded byte lease.
// Resource identity is checked against the authenticated Agent and current
// configuration by the control plane; this wire decoder only validates shape.
type ConnectionOpen struct {
	RequestID      string `json:"request_id"`
	ConnectionID   string `json:"connection_id"`
	ResourceKind   string `json:"resource_kind"`
	ResourceID     string `json:"resource_id"`
	LineID         string `json:"line_id,omitempty"`
	Revision       int64  `json:"revision"`
	RequestedBytes int64  `json:"requested_bytes"`
}

type QuotaRequest struct {
	RequestID      string `json:"request_id"`
	ConnectionID   string `json:"connection_id"`
	Revision       int64  `json:"revision"`
	RequestedBytes int64  `json:"requested_bytes"`
}

// QuotaGrant carries a lease for one connection and billing period. Callers
// must independently check current time: an old, expired retry can decode.
type QuotaGrant struct {
	RequestID       string    `json:"request_id"`
	ConnectionID    string    `json:"connection_id"`
	PeriodID        string    `json:"period_id"`
	MultiplierMilli int64     `json:"multiplier_milli"`
	LeaseID         string    `json:"lease_id"`
	GrantedBytes    int64     `json:"granted_bytes"`
	ConsumedBytes   int64     `json:"consumed_bytes"`
	LeaseState      string    `json:"lease_state"`
	IssuedAt        time.Time `json:"issued_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	PeriodEndsAt    time.Time `json:"period_ends_at"`
}

type QuotaDenied struct {
	RequestID    string `json:"request_id"`
	ConnectionID string `json:"connection_id"`
	ErrorCode    string `json:"error_code"`
}

type QuotaSettle struct {
	RequestID     string `json:"request_id"`
	ConnectionID  string `json:"connection_id"`
	LeaseID       string `json:"lease_id"`
	ConsumedBytes int64  `json:"consumed_bytes"`
}

type QuotaSettled struct {
	RequestID     string `json:"request_id"`
	ConnectionID  string `json:"connection_id"`
	LeaseID       string `json:"lease_id"`
	ConsumedBytes int64  `json:"consumed_bytes"`
}

func decodeQuotaPayload(payload []byte, target any, fields ...string) error {
	if len(payload) > MaxQuotaPayloadBytes {
		return errors.New("quota payload exceeds 4096 bytes")
	}
	if err := decodeStrictPayload(payload, target); err != nil {
		return err
	}
	if !hasExactUsageFields(payload, fields...) {
		return errors.New("invalid quota payload fields")
	}
	return nil
}

func canonicalQuotaUUID(value *string) bool {
	if !uuidPattern.MatchString(*value) {
		return false
	}
	*value = strings.ToLower(*value)
	return true
}

func validQuotaRequestIdentity(requestID, connectionID *string) bool {
	return canonicalQuotaUUID(requestID) && canonicalQuotaUUID(connectionID)
}

func validRequestedBytes(value int64) bool {
	return value >= 1 && value <= MaxQuotaGrantBytes
}

func DecodeConnectionOpen(payload []byte) (ConnectionOpen, error) {
	var value ConnectionOpen
	fields := []string{"request_id", "connection_id", "resource_kind", "resource_id", "revision", "requested_bytes"}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return ConnectionOpen{}, err
	}
	linePresent := false
	if raw, ok := object["line_id"]; ok {
		linePresent = true
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return ConnectionOpen{}, errors.New("invalid connection open line ID")
		}
		fields = append(fields, "line_id")
	}
	if err := decodeQuotaPayload(payload, &value, fields...); err != nil {
		return ConnectionOpen{}, err
	}
	if !validQuotaRequestIdentity(&value.RequestID, &value.ConnectionID) ||
		!canonicalQuotaUUID(&value.ResourceID) ||
		(value.LineID != "" && !canonicalQuotaUUID(&value.LineID)) ||
		(value.ResourceKind != "proxy" && value.LineID != "") ||
		(linePresent && value.LineID == "") ||
		(value.ResourceKind != "forward" && value.ResourceKind != "proxy") ||
		value.Revision <= 0 || !validRequestedBytes(value.RequestedBytes) {
		return ConnectionOpen{}, errors.New("invalid connection open")
	}
	return value, nil
}

func DecodeQuotaRequest(payload []byte) (QuotaRequest, error) {
	var value QuotaRequest
	if err := decodeQuotaPayload(payload, &value, "request_id", "connection_id", "revision", "requested_bytes"); err != nil {
		return QuotaRequest{}, err
	}
	if !validQuotaRequestIdentity(&value.RequestID, &value.ConnectionID) || value.Revision <= 0 || !validRequestedBytes(value.RequestedBytes) {
		return QuotaRequest{}, errors.New("invalid quota request")
	}
	return value, nil
}

func validQuotaTime(value time.Time) bool {
	return !value.IsZero() && value.Nanosecond()%1000 == 0
}

func DecodeQuotaGrant(payload []byte) (QuotaGrant, error) {
	var value QuotaGrant
	if err := decodeQuotaPayload(payload, &value, "request_id", "connection_id", "period_id", "multiplier_milli", "lease_id", "granted_bytes", "consumed_bytes", "lease_state", "issued_at", "expires_at", "period_ends_at"); err != nil {
		return QuotaGrant{}, err
	}
	if !validQuotaRequestIdentity(&value.RequestID, &value.ConnectionID) ||
		!canonicalQuotaUUID(&value.PeriodID) || !canonicalQuotaUUID(&value.LeaseID) ||
		value.MultiplierMilli < 1 || value.MultiplierMilli > 100000 ||
		!validRequestedBytes(value.GrantedBytes) || value.ConsumedBytes < 0 ||
		(value.LeaseState != "active" && value.LeaseState != "settled") ||
		!validQuotaTime(value.IssuedAt) || !validQuotaTime(value.ExpiresAt) || !validQuotaTime(value.PeriodEndsAt) ||
		!value.ExpiresAt.After(value.IssuedAt) || value.ExpiresAt.After(value.PeriodEndsAt) ||
		value.ExpiresAt.Sub(value.IssuedAt) > 30*time.Second {
		return QuotaGrant{}, errors.New("invalid quota grant")
	}
	value.IssuedAt = value.IssuedAt.UTC()
	value.ExpiresAt = value.ExpiresAt.UTC()
	value.PeriodEndsAt = value.PeriodEndsAt.UTC()
	return value, nil
}

func DecodeQuotaDenied(payload []byte) (QuotaDenied, error) {
	var value QuotaDenied
	if err := decodeQuotaPayload(payload, &value, "request_id", "connection_id", "error_code"); err != nil {
		return QuotaDenied{}, err
	}
	if !validQuotaRequestIdentity(&value.RequestID, &value.ConnectionID) {
		return QuotaDenied{}, errors.New("invalid quota denial")
	}
	switch value.ErrorCode {
	case "QUOTA_EXHAUSTED", "NOT_AUTHORIZED", "CONFLICT", "RETRY_LATER":
	default:
		return QuotaDenied{}, errors.New("invalid quota denial code")
	}
	return value, nil
}

func DecodeQuotaSettle(payload []byte) (QuotaSettle, error) {
	var value QuotaSettle
	if err := decodeQuotaPayload(payload, &value, "request_id", "connection_id", "lease_id", "consumed_bytes"); err != nil {
		return QuotaSettle{}, err
	}
	if !validQuotaRequestIdentity(&value.RequestID, &value.ConnectionID) || !canonicalQuotaUUID(&value.LeaseID) || value.ConsumedBytes < 0 {
		return QuotaSettle{}, errors.New("invalid quota settlement")
	}
	return value, nil
}

func DecodeQuotaSettled(payload []byte) (QuotaSettled, error) {
	var value QuotaSettled
	if err := decodeQuotaPayload(payload, &value, "request_id", "connection_id", "lease_id", "consumed_bytes"); err != nil {
		return QuotaSettled{}, err
	}
	if !validQuotaRequestIdentity(&value.RequestID, &value.ConnectionID) || !canonicalQuotaUUID(&value.LeaseID) || value.ConsumedBytes < 0 {
		return QuotaSettled{}, errors.New("invalid quota settlement acknowledgement")
	}
	return value, nil
}
