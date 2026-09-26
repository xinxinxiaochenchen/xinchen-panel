package agentproto

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

const usageConnection = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const usageLease = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

func usageBatchFixture() UsageBatch {
	return UsageBatch{BatchID: testMessageID, Reports: []UsageReport{{ConnectionID: usageConnection, LeaseID: usageLease, Sequence: 1, UploadedBytes: 1024, DownloadedBytes: 2048, ObservedAt: time.Date(2026, 9, 26, 1, 2, 3, 123456000, time.UTC)}}}
}

func TestUsageBatchRoundTripAndStableDigest(t *testing.T) {
	value := usageBatchFixture()
	raw, _ := json.Marshal(value)
	got, err := DecodeUsageBatch(raw)
	if err != nil || len(got.Reports) != 1 || got.Reports[0] != value.Reports[0] {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	digest, err := UsageBatchDigest(value)
	if err != nil || len(digest) != 64 {
		t.Fatalf("digest: %q %v", digest, err)
	}
	value.Reports[0].ConnectionID = strings.ToUpper(value.Reports[0].ConnectionID)
	value.Reports[0].ObservedAt = value.Reports[0].ObservedAt.In(time.FixedZone("offset", 3600))
	same, err := UsageBatchDigest(value)
	if err != nil || same != digest {
		t.Fatalf("equivalent encoding changed digest: %q %v", same, err)
	}
	value.Reports[0].UploadedBytes++
	changed, _ := UsageBatchDigest(value)
	if changed == digest {
		t.Fatal("modified counters retained digest")
	}
	ack := UsageAck{BatchID: value.BatchID, SHA256: digest}
	body, _ := json.Marshal(ack)
	if _, err := DecodeUsageAck(body); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []MessageType{TypeUsageBatch, TypeUsageAck} {
		envelope := validEnvelope()
		envelope.Type = kind
		envelope.Payload = raw
		if _, err := Encode(envelope); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUsageBatchRejectsInvalidOrAmbiguousReports(t *testing.T) {
	raw, _ := json.Marshal(usageBatchFixture())
	for name, mutate := range map[string]func(*UsageBatch){
		"empty batch":        func(v *UsageBatch) { v.Reports = nil },
		"invalid batch ID":   func(v *UsageBatch) { v.BatchID = "bad" },
		"invalid connection": func(v *UsageBatch) { v.Reports[0].ConnectionID = "bad" },
		"invalid lease":      func(v *UsageBatch) { v.Reports[0].LeaseID = "bad" },
		"zero sequence":      func(v *UsageBatch) { v.Reports[0].Sequence = 0 },
		"negative upload":    func(v *UsageBatch) { v.Reports[0].UploadedBytes = -1 },
		"negative download":  func(v *UsageBatch) { v.Reports[0].DownloadedBytes = -1 },
		"overflow counters":  func(v *UsageBatch) { v.Reports[0].UploadedBytes = math.MaxInt64 },
		"missing time":       func(v *UsageBatch) { v.Reports[0].ObservedAt = time.Time{} },
		"duplicate report":   func(v *UsageBatch) { v.Reports = append(v.Reports, v.Reports[0]) },
		"too many reports":   func(v *UsageBatch) { v.Reports = make([]UsageReport, MaxUsageReports+1) },
	} {
		t.Run(name, func(t *testing.T) {
			v := usageBatchFixture()
			mutate(&v)
			b, _ := json.Marshal(v)
			if _, err := DecodeUsageBatch(b); err == nil {
				t.Fatal("accepted invalid usage batch")
			}
		})
	}
	for name, b := range map[string]string{
		"self assigned multiplier": strings.Replace(string(raw), `"sequence":1`, `"sequence":1,"multiplier_milli":1`, 1),
		"self assigned user":       strings.Replace(string(raw), `"sequence":1`, `"sequence":1,"user_id":"`+testNodeID+`"`, 1),
		"missing zero counter":     strings.Replace(string(raw), `"uploaded_bytes":1024,`, ``, 1),
		"null counter":             strings.Replace(string(raw), `"uploaded_bytes":1024`, `"uploaded_bytes":null`, 1),
		"duplicate field":          strings.Replace(string(raw), `"sequence":1`, `"sequence":1,"sequence":2`, 1),
		"case alias field":         strings.Replace(string(raw), `"sequence":1`, `"sequence":1,"Sequence":2`, 1),
		"trailing JSON":            string(raw) + `{}`,
		"oversized":                strings.Repeat(" ", MaxUsagePayloadBytes) + string(raw),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeUsageBatch([]byte(b)); err == nil {
				t.Fatal("accepted malformed usage batch")
			}
		})
	}
}

func TestUsageAckRejectsInvalidPayload(t *testing.T) {
	for _, raw := range []string{`{}`, `{"batch_id":"` + testMessageID + `","sha256":"bad"}`, `{"batch_id":"` + testMessageID + `","sha256":"` + strings.Repeat("a", 64) + `","accepted":true}`} {
		if _, err := DecodeUsageAck([]byte(raw)); err == nil {
			t.Fatal("accepted invalid ACK")
		}
	}
}
