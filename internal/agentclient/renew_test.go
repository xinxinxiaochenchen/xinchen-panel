package agentclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentClientReloadsCertificateAfterRenewal(t *testing.T) {
	issuer, caPEM := testEnrollmentIssuer(t)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, private)
	if err != nil {
		t.Fatal(err)
	}
	csr := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	first, err := issuer.IssueClientCertificate(csr, testNodeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := issuer.IssueClientCertificate(csr, testNodeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key")
	if err := os.WriteFile(certPath, first.CertificatePEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{URL: "wss://127.0.0.1:18443/api/v1/agent/stream", NodeID: testNodeID,
		Version: "v1", RootCAs: roots, Certificate: pair, CertFile: certPath, KeyFile: keyPath},
		func() Runtime { return &fakeRuntime{} }, &fakeStateStore{})
	if err != nil {
		t.Fatal(err)
	}
	transport := client.httpClient.Transport.(*http.Transport)
	loaded, err := transport.TLSClientConfig.GetClientCertificate(nil)
	if err != nil || string(loaded.Certificate[0]) != string(pair.Certificate[0]) {
		t.Fatalf("initial certificate: %v", err)
	}
	if err := SaveRenewedCertificate(certPath, keyPath, second.CertificatePEM); err != nil {
		t.Fatal(err)
	}
	loaded, err = transport.TLSClientConfig.GetClientCertificate(nil)
	if err != nil || string(loaded.Certificate[0]) == string(pair.Certificate[0]) {
		t.Fatalf("renewed certificate: %v", err)
	}
}

func TestSaveRenewedCertificateReplacesOnlyCertificate(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key")
	pair := testClientCertificate(t)
	keyBytes, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]})
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveRenewedCertificate(certPath, keyPath, []byte("invalid")); err == nil {
		t.Fatal("invalid certificate accepted")
	}
	stored, _ := os.ReadFile(certPath)
	if string(stored) != string(certPEM) {
		t.Fatal("failed renewal replaced certificate")
	}
	if err := SaveRenewedCertificate(certPath, keyPath, certPEM); err != nil {
		t.Fatal(err)
	}
	storedKey, _ := os.ReadFile(keyPath)
	if string(storedKey) != string(keyPEM) {
		t.Fatal("renewal changed private key")
	}
	info, err := os.Stat(certPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("certificate permissions: %v %v", info, err)
	}
}

func TestRenewCertificateUsesCurrentKeyAndValidatesResponse(t *testing.T) {
	issuer, caPEM := testEnrollmentIssuer(t)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, private)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := issuer.IssueClientCertificate(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), testNodeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(initial.CertificatePEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
			t.Error("renewal lacks client certificate")
			return
		}
		var input struct {
			CSRPEM  string `json:"csr_pem"`
			Version string `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if input.Version != "v1" {
			t.Errorf("version %s", input.Version)
		}
		issued, err := issuer.IssueClientCertificate([]byte(input.CSRPEM), testNodeID, time.Now())
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := tls.X509KeyPair(issued.CertificatePEM, keyPEM); err != nil {
			t.Error("CSR key changed")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"node_id": testNodeID, "certificate_pem": string(issued.CertificatePEM), "ca_certificate_pem": string(caPEM), "expires_at": issued.ExpiresAt})
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	roots.AppendCertsFromPEM(caPEM)
	renewed, err := RenewCertificate(context.Background(), server.URL+"/api/v1/agent/renew", roots, pair, keyPEM, "v1", testNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(renewed.CertPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	if !renewed.ExpiresAt.After(time.Now().Add(23 * time.Hour)) {
		t.Fatalf("renewed expiry: %s", renewed.ExpiresAt)
	}
	if _, err := RenewCertificate(context.Background(), "http"+strings.TrimPrefix(server.URL, "https")+"/api/v1/agent/renew", roots, pair, keyPEM, "v1", testNodeID); err == nil {
		t.Fatal("plaintext renewal accepted")
	}
}
