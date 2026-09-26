package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentidentity"
)

type fakeEnrollment struct {
	result agentidentity.EnrollmentResult
	err    error
	called int
}

func (fake *fakeEnrollment) Enroll(_ context.Context, token string, csr []byte, version string) (agentidentity.EnrollmentResult, error) {
	fake.called++
	if token != "secret-token" || string(csr) != "csr-pem" || version != "v1.0" {
		return agentidentity.EnrollmentResult{}, errors.New("unexpected enrollment input")
	}
	return fake.result, fake.err
}

func TestAgentEnrollmentRequiresTLSAndReturnsCertificate(t *testing.T) {
	service := &fakeEnrollment{result: agentidentity.EnrollmentResult{NodeID: certificateTestNodeID,
		CertificatePEM: []byte("cert-pem"), CACertificatePEM: []byte("ca-pem"), ExpiresAt: time.Now().Add(time.Hour)}}
	handler := NewAgentHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), service)
	requestBody := `{"token":"secret-token","csr_pem":"csr-pem","version":"v1.0"}`
	plain := httptest.NewServer(handler)
	defer plain.Close()
	response, err := http.Post(plain.URL+"/api/v1/agent/enroll", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound || service.called != 0 {
		t.Fatalf("plain HTTP enrollment = %d, calls=%d", response.StatusCode, service.called)
	}
	secure := httptest.NewTLSServer(handler)
	defer secure.Close()
	response, err = secure.Client().Post(secure.URL+"/api/v1/agent/enroll", "application/json", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated || service.called != 1 {
		t.Fatalf("TLS enrollment = %d, calls=%d", response.StatusCode, service.called)
	}
	var body struct {
		NodeID           string `json:"node_id"`
		CertificatePEM   string `json:"certificate_pem"`
		CACertificatePEM string `json:"ca_certificate_pem"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.NodeID != certificateTestNodeID || body.CertificatePEM != "cert-pem" || body.CACertificatePEM != "ca-pem" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("enrollment response = %+v", body)
	}
}

func TestAgentEnrollmentRejectsMalformedInputAndInvalidToken(t *testing.T) {
	service := &fakeEnrollment{err: agentidentity.ErrInvalidEnrollment}
	secure := httptest.NewTLSServer(NewAgentHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), service))
	defer secure.Close()
	for _, body := range []string{`{}`, `{"token":"secret-token","csr_pem":"csr-pem","version":"v1.0","extra":1}`, `{"token":"secret-token","csr_pem":"csr-pem","version":"v1.0"} {}`} {
		response, err := secure.Client().Post(secure.URL+"/api/v1/agent/enroll", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("malformed enrollment %s = %d", body, response.StatusCode)
		}
	}
	request := bytes.NewBufferString(`{"token":"secret-token","csr_pem":"csr-pem","version":"v1.0"}`)
	response, err := secure.Client().Post(secure.URL+"/api/v1/agent/enroll", "application/json", request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || service.called != 1 {
		t.Fatalf("invalid token = %d, calls=%d", response.StatusCode, service.called)
	}
}

func TestAgentEnrollmentDistinguishesInvalidCSRFromInternalFailure(t *testing.T) {
	service := &fakeEnrollment{err: agentidentity.ErrInvalidCSR}
	secure := httptest.NewTLSServer(NewAgentHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), service))
	defer secure.Close()
	body := `{"token":"secret-token","csr_pem":"csr-pem","version":"v1.0"}`
	response, err := secure.Client().Post(secure.URL+"/api/v1/agent/enroll", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid CSR = %d", response.StatusCode)
	}
	service.err = errors.New("database unavailable")
	response, err = secure.Client().Post(secure.URL+"/api/v1/agent/enroll", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("internal enrollment failure = %d", response.StatusCode)
	}
}

func TestAgentEnrollmentLimitsRepeatedAttemptsBeforeStore(t *testing.T) {
	service := &fakeEnrollment{err: agentidentity.ErrInvalidEnrollment}
	handler := NewAgentHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), service)
	body := `{"token":"secret-token","csr_pem":"csr-pem","version":"v1.0"}`
	limited := false
	for range 7 {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enroll", strings.NewReader(body))
		request.TLS = &tls.ConnectionState{}
		request.RemoteAddr = "192.0.2.4:12345"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusTooManyRequests {
			limited = true
			if response.Header().Get("Retry-After") == "" {
				t.Fatal("missing Retry-After")
			}
		}
	}
	if !limited || service.called > 5 {
		t.Fatalf("rate limit ineffective: limited=%v calls=%d", limited, service.called)
	}
}

const certificateTestNodeID = "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
