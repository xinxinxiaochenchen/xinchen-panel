package agentproto

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

const testNodeID = "11111111-1111-4111-8111-111111111111"
const testMessageID = "22222222-2222-4222-8222-222222222222"

func validEnvelope() Envelope {
	return Envelope{
		ProtocolVersion: 1,
		MessageID:       testMessageID,
		NodeID:          testNodeID,
		Type:            TypeHello,
		SentAt:          time.Date(2026, 9, 26, 1, 2, 3, 0, time.UTC),
		Payload:         json.RawMessage(`{"agent_version":"1.0"}`),
	}
}

func TestEnvelopeCodecRoundTrip(t *testing.T) {
	encoded, err := Encode(validEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded)
	if err != nil || decoded.Type != TypeHello || decoded.NodeID != testNodeID ||
		!bytes.Equal(decoded.Payload, []byte(`{"agent_version":"1.0"}`)) {
		t.Fatalf("round trip = %+v, %v", decoded, err)
	}
}

func TestEnvelopeCodecRejectsInvalidAndAmbiguousFrames(t *testing.T) {
	valid, err := Encode(validEnvelope())
	if err != nil {
		t.Fatal(err)
	}
	for name, frame := range map[string][]byte{
		"unsupported version": bytes.Replace(valid, []byte(`"protocol_version":1`), []byte(`"protocol_version":2`), 1),
		"unknown type":        bytes.Replace(valid, []byte(`"type":"hello"`), []byte(`"type":"surprise"`), 1),
		"unknown field":       append(bytes.TrimSuffix(valid, []byte("}")), []byte(`,"extra":1}`)...),
		"trailing JSON":       append(append([]byte(nil), valid...), []byte(`{}`)...),
		"duplicate field":     append(bytes.TrimSuffix(valid, []byte("}")), []byte(`,"node_id":"`+testNodeID+`"}`)...),
		"duplicate payload":   bytes.Replace(valid, []byte(`"agent_version":"1.0"`), []byte(`"agent_version":"1.0","agent_version":"2.0"`), 1),
		"oversized":           bytes.Repeat([]byte("x"), MaxFrameBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(frame); err == nil {
				t.Fatalf("accepted invalid frame %q", frame[:min(len(frame), 80)])
			}
		})
	}
	for name, mutate := range map[string]func(*Envelope){
		"missing node":      func(value *Envelope) { value.NodeID = "" },
		"invalid ID":        func(value *Envelope) { value.MessageID = "not-an-id" },
		"missing payload":   func(value *Envelope) { value.Payload = nil },
		"zero timestamp":    func(value *Envelope) { value.SentAt = time.Time{} },
		"duplicate payload": func(value *Envelope) { value.Payload = json.RawMessage(`{"a":1,"a":2}`) },
	} {
		t.Run(name, func(t *testing.T) {
			value := validEnvelope()
			mutate(&value)
			if _, err := Encode(value); err == nil {
				t.Fatal("encoded invalid envelope")
			}
		})
	}
}
