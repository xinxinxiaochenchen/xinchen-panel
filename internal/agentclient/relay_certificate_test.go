package agentclient

import (
	"bytes"
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
	"testing"
	"time"

	"controlplane/internal/agentidentity"
)

func TestRequestRelayCertificateValidatesServerIdentity(t *testing.T) {
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
	initial, err := issuer.IssueClientCertificate(csr, testNodeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(private)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(initial.CertificatePEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	relayKeyPub, relayKey, _ := ed25519.GenerateKey(rand.Reader)
	_ = relayKeyPub
	relayKeyDER, _ := x509.MarshalPKCS8PrivateKey(relayKey)
	relayKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: relayKeyDER})
	relayCSRDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, relayKey)
	relayIssued, err := issuer.IssueRelayServerCertificate(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: relayCSRDER}), testNodeID, "127.0.0.1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input["parent_fingerprint"] != "old" {
			t.Errorf("parent=%q", input["parent_fingerprint"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"node_id": testNodeID, "certificate_pem": string(relayIssued.CertificatePEM), "ca_certificate_pem": string(caPEM), "fingerprint": relayIssued.Fingerprint, "expires_at": relayIssued.ExpiresAt})
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	caBlock, _ := pem.Decode(caPEM)
	caCert, _ := x509.ParseCertificate(caBlock.Bytes)
	roots.AddCert(caCert)
	got, err := RequestRelayCertificate(context.Background(), server.URL+"/api/v1/agent/relay-certificate", roots, pair, keyPEM, relayKeyPEM, "old", testNodeID, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != testNodeID {
		t.Fatalf("bad result: %+v", got)
	}
	if _, err := agentidentity.CertificateRelayNodeID(parseClientCert(t, got.CertPEM)); err != nil {
		t.Fatal(err)
	}
	if _, err := RequestRelayCertificate(context.Background(), server.URL+"/wrong", roots, pair, keyPEM, relayKeyPEM, "old", testNodeID, "127.0.0.1"); err == nil {
		t.Fatal("wrong endpoint accepted")
	}
}
func parseClientCert(t *testing.T, b []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(b)
	c, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestEnsureRelayKeyPersistsForRetryAndCertificateSave(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "relay.key")
	certPath := filepath.Join(dir, "relay.crt")
	first, err := EnsureRelayKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureRelayKey(keyPath)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("retry lost key: %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("key mode=%v %v", info, err)
	}
	issuer, _ := testEnrollmentIssuer(t)
	block, _ := pem.Decode(first)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, parsed.(ed25519.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := issuer.IssueRelayServerCertificate(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), testNodeID, "relay.example.com", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRelayCertificate(certPath, keyPath, cert.CertificatePEM); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(certPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("cert mode=%v %v", info, err)
	}
	if err := SaveRelayCertificate(certPath, keyPath, []byte("bad")); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	stored, _ := os.ReadFile(certPath)
	if !bytes.Equal(stored, cert.CertificatePEM) {
		t.Fatal("bad certificate replaced old cert")
	}
	if err := os.WriteFile(keyPath, append(first, []byte("garbage")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureRelayKey(keyPath); err == nil {
		t.Fatal("relay key with trailing data accepted")
	}
	if err := os.WriteFile(keyPath, first, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureRelayKey(keyPath); err == nil {
		t.Fatal("permissive relay key accepted")
	}
}
