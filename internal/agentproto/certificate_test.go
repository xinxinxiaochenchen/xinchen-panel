package agentproto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCertificateUpdateAndAckRejectAmbiguousPayloads(t *testing.T) {
	validUpdate := `{"certificate_pem":"-----BEGIN CERTIFICATE-----\\nabc\\n-----END CERTIFICATE-----\\n"}`
	if _, err := DecodeCertificateUpdate([]byte(validUpdate)); err != nil {
		t.Fatalf("valid update: %v", err)
	}
	for _, raw := range []string{
		`{}`,
		`{"certificate_pem":""}`,
		`{"certificate_pem":"cert","extra":true}`,
		`{"certificate_pem":"a","certificate_pem":"b"}`,
		`{"Certificate_Pem":"cert"}`,
	} {
		if _, err := DecodeCertificateUpdate([]byte(raw)); err == nil {
			t.Fatalf("accepted update %s", raw)
		}
	}
	goodDigest := strings.Repeat("a", 64)
	ackPayload, _ := json.Marshal(CertificateUpdateAck{Fingerprint: goodDigest})
	if _, err := DecodeCertificateUpdateAck(ackPayload); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{}`,
		`{"fingerprint":"ABC"}`,
		`{"fingerprint":"` + goodDigest + `","fingerprint":"` + goodDigest + `"}`,
		`{"fingerprint":"` + goodDigest + `","extra":1}`,
	} {
		if _, err := DecodeCertificateUpdateAck([]byte(raw)); err == nil {
			t.Fatalf("accepted acknowledgement %s", raw)
		}
	}
	frame := validEnvelope()
	frame.Type = TypeCertificateUpdate
	frame.Payload = []byte(validUpdate)
	if _, err := Encode(frame); err != nil {
		t.Fatalf("certificate update envelope: %v", err)
	}
	frame.Type = TypeCertificateUpdateAck
	frame.Payload = ackPayload
	if _, err := Encode(frame); err != nil {
		t.Fatalf("certificate ack envelope: %v", err)
	}
}
