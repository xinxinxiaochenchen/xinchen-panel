package agentproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
)

const MaxFrameBytes = 1 << 20
const ProtocolVersion = 1

type MessageType string

const (
	TypeHello          MessageType = "hello"
	TypeHeartbeat      MessageType = "heartbeat"
	TypeConfigSnapshot MessageType = "config_snapshot"
	TypeConfigResult   MessageType = "config_result"
	TypeUsageBatch     MessageType = "usage_batch"
	TypeUsageAck       MessageType = "usage_ack"
	TypeConnectionOpen MessageType = "connection_open"
	TypeQuotaRequest   MessageType = "quota_request"
	TypeQuotaGrant     MessageType = "quota_grant"
	TypeQuotaDenied    MessageType = "quota_denied"
	TypeQuotaSettle    MessageType = "quota_settle"
	TypeQuotaSettled   MessageType = "quota_settled"
)

type Envelope struct {
	ProtocolVersion int             `json:"protocol_version"`
	MessageID       string          `json:"message_id"`
	NodeID          string          `json:"node_id"`
	Type            MessageType     `json:"type"`
	SentAt          time.Time       `json:"sent_at"`
	Payload         json.RawMessage `json:"payload"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (value Envelope) validate() error {
	if value.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported Agent protocol version %d", value.ProtocolVersion)
	}
	if !uuidPattern.MatchString(value.MessageID) || !uuidPattern.MatchString(value.NodeID) {
		return errors.New("message_id and node_id must be UUIDs")
	}
	switch value.Type {
	case TypeHello, TypeHeartbeat, TypeConfigSnapshot, TypeConfigResult, TypeUsageBatch, TypeUsageAck,
		TypeConnectionOpen, TypeQuotaRequest, TypeQuotaGrant, TypeQuotaDenied, TypeQuotaSettle, TypeQuotaSettled:
	default:
		return fmt.Errorf("unsupported Agent message type %q", value.Type)
	}
	if value.SentAt.IsZero() || len(value.Payload) == 0 || !json.Valid(value.Payload) ||
		len(bytes.TrimSpace(value.Payload)) == 0 || bytes.TrimSpace(value.Payload)[0] != '{' {
		return errors.New("sent_at and object payload are required")
	}
	if err := rejectDuplicateEnvelopeFields(value.Payload); err != nil {
		return fmt.Errorf("invalid Agent payload: %w", err)
	}
	return nil
}

func Encode(value Envelope) ([]byte, error) {
	if err := value.validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode Agent envelope: %w", err)
	}
	if len(encoded) > MaxFrameBytes {
		return nil, errors.New("Agent frame exceeds 1 MiB")
	}
	return encoded, nil
}

func Decode(frame []byte) (Envelope, error) {
	if len(frame) > MaxFrameBytes {
		return Envelope{}, errors.New("Agent frame exceeds 1 MiB")
	}
	if err := rejectDuplicateEnvelopeFields(frame); err != nil {
		return Envelope{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	var value Envelope
	if err := decoder.Decode(&value); err != nil {
		return Envelope{}, fmt.Errorf("decode Agent envelope: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Envelope{}, errors.New("Agent frame has trailing JSON")
	}
	if err := value.validate(); err != nil {
		return Envelope{}, err
	}
	return value, nil
}

func rejectDuplicateEnvelopeFields(frame []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(frame))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("Agent envelope must be a JSON object")
	}
	return scanObject(decoder, 0)
}

func scanValue(decoder *json.Decoder, depth int) error {
	if depth >= 64 {
		return errors.New("Agent JSON nesting exceeds 64 levels")
	}
	value, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read Agent JSON value: %w", err)
	}
	switch value {
	case json.Delim('{'):
		return scanObject(decoder, depth+1)
	case json.Delim('['):
		for decoder.More() {
			if err := scanValue(decoder, depth+1); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("close Agent JSON array: %w", err)
		}
	}
	return nil
}

func scanObject(decoder *json.Decoder, depth int) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read Agent envelope field: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("Agent envelope field name must be a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate Agent envelope field %q", key)
		}
		seen[key] = struct{}{}
		if err := scanValue(decoder, depth+1); err != nil {
			return fmt.Errorf("read Agent envelope field %q: %w", key, err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("close Agent envelope: %w", err)
	}
	return nil
}
