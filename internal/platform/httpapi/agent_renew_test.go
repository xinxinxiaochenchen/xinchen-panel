package httpapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentidentity"
)

type renewalServiceStub struct{ calls int }

func (s *renewalServiceStub) Enroll(context.Context, string, []byte, string) (agentidentity.EnrollmentResult, error) {
	return agentidentity.EnrollmentResult{}, nil
}
func (s *renewalServiceStub) Renew(_ context.Context, cert *x509.Certificate, csr []byte, version string) (agentidentity.EnrollmentResult, error) {
	s.calls++
	if cert == nil || string(csr) != "csr" || version != "v1" {
		return agentidentity.EnrollmentResult{}, agentidentity.ErrInvalidCSR
	}
	return agentidentity.EnrollmentResult{NodeID: "node", CertificatePEM: []byte("cert"), CACertificatePEM: []byte("ca"), ExpiresAt: time.Now()}, nil
}

func TestAgentRenewalRequiresVerifiedClientCertificate(t *testing.T) {
	service := &renewalServiceStub{}
	handler := NewAgentHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), service)
	body := `{"csr_pem":"csr","version":"v1"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/renew", strings.NewReader(body))
	request.TLS = &tls.ConnectionState{}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || service.calls != 0 {
		t.Fatalf("missing certificate: %d calls=%d", recorder.Code, service.calls)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/agent/renew", strings.NewReader(body))
	request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}, VerifiedChains: [][]*x509.Certificate{{{}}}}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.calls != 1 || !strings.Contains(recorder.Body.String(), `"certificate_pem":"cert"`) {
		t.Fatalf("authenticated renewal: %d %s calls=%d", recorder.Code, recorder.Body.String(), service.calls)
	}
}
