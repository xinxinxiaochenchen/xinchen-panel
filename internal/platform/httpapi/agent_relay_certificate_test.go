package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"controlplane/internal/agentidentity"
)

type relayCertificateStub struct {
	calls int
	err   error
}

func (s *relayCertificateStub) Enroll(context.Context, string, []byte, string) (agentidentity.EnrollmentResult, error) {
	return agentidentity.EnrollmentResult{}, nil
}
func (s *relayCertificateStub) RelayCertificate(_ context.Context, cert *x509.Certificate, csr []byte, parent string) (agentidentity.EnrollmentResult, error) {
	s.calls++
	if cert == nil || string(csr) != "csr" || parent != "" {
		return agentidentity.EnrollmentResult{}, agentidentity.ErrInvalidCSR
	}
	return agentidentity.EnrollmentResult{NodeID: "node", CertificatePEM: []byte("cert"), CACertificatePEM: []byte("ca")}, s.err
}
func TestRelayCertificateEndpoint(t *testing.T) {
	verified := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}, VerifiedChains: [][]*x509.Certificate{{{}}}}
	for _, tt := range []struct {
		name, body, method string
		tls                *tls.ConnectionState
		want, calls        int
	}{
		{"plain", `{"csr_pem":"csr"}`, "POST", nil, 404, 0},
		{"unverified", `{"csr_pem":"csr"}`, "POST", &tls.ConnectionState{}, 401, 0},
		{"method", `{"csr_pem":"csr"}`, "GET", verified, 405, 0},
		{"unknown host", `{"csr_pem":"csr","host":"forged.example.com"}`, "POST", verified, 400, 0},
		{"trailing", `{"csr_pem":"csr"} {}`, "POST", verified, 400, 0},
		{"empty", `{}`, "POST", verified, 400, 0},
		{"issue", `{"csr_pem":"csr"}`, "POST", verified, 200, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &relayCertificateStub{}
			h := NewAgentHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), s)
			r := httptest.NewRequest(tt.method, "/api/v1/agent/relay-certificate", strings.NewReader(tt.body))
			r.TLS = tt.tls
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.want || s.calls != tt.calls {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, s.calls, w.Body.String())
			}
			if tt.want == 200 && (w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"certificate_pem":"cert"`)) {
				t.Fatal("missing certificate or cache guard")
			}
		})
	}
}
