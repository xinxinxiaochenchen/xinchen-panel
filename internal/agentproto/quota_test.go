package agentproto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const quotaRequestID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
const quotaResourceID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
const quotaPeriodID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"

func quotaFixtureTimes() (time.Time, time.Time, time.Time) {
	issued := time.Date(2026, 9, 26, 1, 2, 3, 123456000, time.UTC)
	return issued, issued.Add(30 * time.Second), issued.Add(time.Hour)
}

func TestQuotaPayloadRoundTrip(t *testing.T) {
	issued, expires, periodEnds := quotaFixtureTimes()
	tests := []struct {
		name     string
		typeName MessageType
		value    any
		decode   func([]byte) (any, error)
	}{
		{"open", TypeConnectionOpen, ConnectionOpen{RequestID: quotaRequestID, ConnectionID: usageConnection, ResourceKind: "proxy", ResourceID: quotaResourceID, Revision: 3, RequestedBytes: 1024}, func(raw []byte) (any, error) { return DecodeConnectionOpen(raw) }},
		{"request", TypeQuotaRequest, QuotaRequest{quotaRequestID, usageConnection, 3, 1024}, func(raw []byte) (any, error) { return DecodeQuotaRequest(raw) }},
		{"grant", TypeQuotaGrant, QuotaGrant{quotaRequestID, usageConnection, quotaPeriodID, 1500, usageLease, 1024, 10, "active", issued, expires, periodEnds}, func(raw []byte) (any, error) { return DecodeQuotaGrant(raw) }},
		{"denied", TypeQuotaDenied, QuotaDenied{quotaRequestID, usageConnection, "QUOTA_EXHAUSTED"}, func(raw []byte) (any, error) { return DecodeQuotaDenied(raw) }},
		{"settle", TypeQuotaSettle, QuotaSettle{quotaRequestID, usageConnection, usageLease, 10}, func(raw []byte) (any, error) { return DecodeQuotaSettle(raw) }},
		{"settled", TypeQuotaSettled, QuotaSettled{quotaRequestID, usageConnection, usageLease, 10}, func(raw []byte) (any, error) { return DecodeQuotaSettled(raw) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			got, err := tt.decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			back, err := json.Marshal(got)
			if err != nil || string(back) != string(raw) {
				t.Fatalf("round trip: %s, %v", back, err)
			}
			envelope := validEnvelope()
			envelope.Type = tt.typeName
			envelope.Payload = raw
			if _, err := Encode(envelope); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQuotaPayloadRejectsMissingUnknownNullDuplicateAndCaseAliases(t *testing.T) {
	issued, expires, periodEnds := quotaFixtureTimes()
	tests := []struct {
		name   string
		value  any
		decode func([]byte) error
		field  string
	}{
		{"open", ConnectionOpen{RequestID: quotaRequestID, ConnectionID: usageConnection, ResourceKind: "forward", ResourceID: quotaResourceID, Revision: 1, RequestedBytes: 1}, func(b []byte) error { _, e := DecodeConnectionOpen(b); return e }, "revision"},
		{"request", QuotaRequest{quotaRequestID, usageConnection, 1, 1}, func(b []byte) error { _, e := DecodeQuotaRequest(b); return e }, "revision"},
		{"grant", QuotaGrant{quotaRequestID, usageConnection, quotaPeriodID, 1000, usageLease, 1, 0, "active", issued, expires, periodEnds}, func(b []byte) error { _, e := DecodeQuotaGrant(b); return e }, "consumed_bytes"},
		{"denied", QuotaDenied{quotaRequestID, usageConnection, "RETRY_LATER"}, func(b []byte) error { _, e := DecodeQuotaDenied(b); return e }, "error_code"},
		{"settle", QuotaSettle{quotaRequestID, usageConnection, usageLease, 0}, func(b []byte) error { _, e := DecodeQuotaSettle(b); return e }, "consumed_bytes"},
		{"settled", QuotaSettled{quotaRequestID, usageConnection, usageLease, 0}, func(b []byte) error { _, e := DecodeQuotaSettled(b); return e }, "consumed_bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.value)
			needle := `"` + tt.field + `":`
			start := strings.Index(string(raw), needle)
			if start < 0 {
				t.Fatal("missing test field")
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatal(err)
			}
			delete(obj, tt.field)
			missing, _ := json.Marshal(obj)
			obj[tt.field] = json.RawMessage("null")
			nulled, _ := json.Marshal(obj)
			cases := [][]byte{
				missing, nulled,
				[]byte(strings.Replace(string(raw), needle, `"unknown_field":true,`+needle, 1)),
				[]byte(strings.Replace(string(raw), needle, needle+`1,"`+tt.field+`":`, 1)),
				[]byte(strings.Replace(string(raw), needle, `"`+strings.ToUpper(tt.field[:1])+tt.field[1:]+`":1,`+needle, 1)),
				append(append([]byte(nil), raw...), []byte("{}")...),
				[]byte(strings.Repeat(" ", MaxQuotaPayloadBytes) + string(raw)),
			}
			for i, candidate := range cases {
				if err := tt.decode(candidate); err == nil {
					t.Errorf("accepted malformed case %d: %.120s", i, candidate)
				}
			}
		})
	}
}

func TestQuotaPayloadValidationBoundaries(t *testing.T) {
	issued, expires, periodEnds := quotaFixtureTimes()
	open := ConnectionOpen{RequestID: quotaRequestID, ConnectionID: usageConnection, ResourceKind: "proxy", ResourceID: quotaResourceID, Revision: 1, RequestedBytes: 1}
	for _, mutate := range []func(*ConnectionOpen){
		func(v *ConnectionOpen) { v.RequestID = "invalid" },
		func(v *ConnectionOpen) { v.ConnectionID = "invalid" },
		func(v *ConnectionOpen) { v.ResourceID = "invalid" },
		func(v *ConnectionOpen) { v.ResourceKind = "node" },
		func(v *ConnectionOpen) { v.Revision = 0 },
		func(v *ConnectionOpen) { v.RequestedBytes = 0 },
		func(v *ConnectionOpen) { v.RequestedBytes = MaxQuotaGrantBytes + 1 },
	} {
		v := open
		mutate(&v)
		b, _ := json.Marshal(v)
		if _, err := DecodeConnectionOpen(b); err == nil {
			t.Errorf("accepted open %+v", v)
		}
	}
	request := QuotaRequest{quotaRequestID, usageConnection, 1, MaxQuotaGrantBytes}
	if b, _ := json.Marshal(request); b != nil {
		if _, err := DecodeQuotaRequest(b); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*QuotaRequest){func(v *QuotaRequest) { v.Revision = 0 }, func(v *QuotaRequest) { v.RequestedBytes = 0 }, func(v *QuotaRequest) { v.RequestedBytes = MaxQuotaGrantBytes + 1 }} {
		v := request
		mutate(&v)
		b, _ := json.Marshal(v)
		if _, err := DecodeQuotaRequest(b); err == nil {
			t.Errorf("accepted request %+v", v)
		}
	}
	grant := QuotaGrant{quotaRequestID, usageConnection, quotaPeriodID, 1000, usageLease, 1, 0, "active", issued, expires, periodEnds}
	for _, mutate := range []func(*QuotaGrant){
		func(v *QuotaGrant) { v.PeriodID = "invalid" },
		func(v *QuotaGrant) { v.LeaseID = "invalid" },
		func(v *QuotaGrant) { v.MultiplierMilli = 0 },
		func(v *QuotaGrant) { v.MultiplierMilli = 100001 },
		func(v *QuotaGrant) { v.GrantedBytes = 0 },
		func(v *QuotaGrant) { v.GrantedBytes = MaxQuotaGrantBytes + 1 },
		func(v *QuotaGrant) { v.ConsumedBytes = -1 },
		func(v *QuotaGrant) { v.LeaseState = "expired" },
		func(v *QuotaGrant) { v.ExpiresAt = v.IssuedAt },
		func(v *QuotaGrant) { v.ExpiresAt = v.PeriodEndsAt.Add(time.Microsecond) },
		func(v *QuotaGrant) { v.ExpiresAt = v.IssuedAt.Add(30*time.Second + time.Microsecond) },
		func(v *QuotaGrant) { v.IssuedAt = v.IssuedAt.Add(time.Nanosecond) },
		func(v *QuotaGrant) { v.ExpiresAt = v.ExpiresAt.Add(time.Nanosecond) },
		func(v *QuotaGrant) { v.PeriodEndsAt = v.PeriodEndsAt.Add(time.Nanosecond) },
	} {
		v := grant
		mutate(&v)
		b, _ := json.Marshal(v)
		if _, err := DecodeQuotaGrant(b); err == nil {
			t.Errorf("accepted grant %+v", v)
		}
	}
	grant.LeaseState = "settled"
	grant.ExpiresAt = issued.Add(time.Second) // an expired retry must still decode.
	b, _ := json.Marshal(grant)
	if _, err := DecodeQuotaGrant(b); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"NOT_AUTHORIZED", "CONFLICT", "RETRY_LATER"} {
		v := QuotaDenied{quotaRequestID, usageConnection, code}
		b, _ := json.Marshal(v)
		if _, err := DecodeQuotaDenied(b); err != nil {
			t.Fatal(err)
		}
	}
	v := QuotaDenied{quotaRequestID, usageConnection, "INTERNAL"}
	b, _ = json.Marshal(v)
	if _, err := DecodeQuotaDenied(b); err == nil {
		t.Fatal("accepted unknown denial code")
	}
	for _, negative := range []any{QuotaSettle{quotaRequestID, usageConnection, usageLease, -1}, QuotaSettled{quotaRequestID, usageConnection, usageLease, -1}} {
		b, _ := json.Marshal(negative)
		switch negative.(type) {
		case QuotaSettle:
			if _, err := DecodeQuotaSettle(b); err == nil {
				t.Fatal("accepted negative settle")
			}
		case QuotaSettled:
			if _, err := DecodeQuotaSettled(b); err == nil {
				t.Fatal("accepted negative settled")
			}
		}
	}
}

func TestConnectionOpenAcceptsExplicitProxyLineAndRejectsForwardLine(t *testing.T) {
	open := ConnectionOpen{RequestID: quotaRequestID, ConnectionID: usageConnection, ResourceKind: "proxy", ResourceID: quotaResourceID,
		LineID: quotaPeriodID, Revision: 1, RequestedBytes: 1}
	payload, _ := json.Marshal(open)
	decoded, err := DecodeConnectionOpen(payload)
	if err != nil || decoded.LineID != quotaPeriodID {
		t.Fatalf("proxy line = %+v, %v", decoded, err)
	}
	open.ResourceKind = "forward"
	payload, _ = json.Marshal(open)
	if _, err := DecodeConnectionOpen(payload); err == nil {
		t.Fatal("forward request accepted Agent-selected line")
	}
	open.ResourceKind = "proxy"
	open.LineID = "bad"
	payload, _ = json.Marshal(open)
	if _, err := DecodeConnectionOpen(payload); err == nil {
		t.Fatal("proxy request accepted invalid line ID")
	}
}

func TestQuotaPayloadCanonicalizesUUIDAndTime(t *testing.T) {
	issued, expires, periodEnds := quotaFixtureTimes()
	v := QuotaGrant{strings.ToUpper(quotaRequestID), strings.ToUpper(usageConnection), strings.ToUpper(quotaPeriodID), 1000, strings.ToUpper(usageLease), 1, 0, "active", issued.In(time.FixedZone("offset", 3600)), expires.In(time.FixedZone("offset", 3600)), periodEnds.In(time.FixedZone("offset", 3600))}
	b, _ := json.Marshal(v)
	got, err := DecodeQuotaGrant(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestID != quotaRequestID || got.ConnectionID != usageConnection || got.PeriodID != quotaPeriodID || got.LeaseID != usageLease || got.IssuedAt.Location() != time.UTC || !got.IssuedAt.Equal(issued) {
		t.Fatalf("not canonical: %+v", got)
	}
}
