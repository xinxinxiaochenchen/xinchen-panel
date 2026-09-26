package agentclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"controlplane/internal/agentidentity"
)

func testEnrollmentIssuer(t *testing.T) (*agentidentity.Issuer, []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Enrollment test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	issuer, err := agentidentity.NewIssuer(caPEM,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	if err != nil {
		t.Fatal(err)
	}
	return issuer, caPEM
}

func TestEnrollGeneratesLocalKeyAndValidatesCertificate(t *testing.T) {
	issuer, caPEM := testEnrollmentIssuer(t)
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Token   string `json:"token"`
			CSRPEM  string `json:"csr_pem"`
			Version string `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if input.Token != token || input.Version != "1.0.0" {
			t.Errorf("request metadata = %+v", input)
			return
		}
		issued, err := issuer.IssueClientCertificate([]byte(input.CSRPEM), testNodeID, time.Now())
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"node_id": testNodeID,
			"certificate_pem": string(issued.CertificatePEM), "ca_certificate_pem": string(caPEM),
			"expires_at": issued.ExpiresAt})
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	credentials, err := Enroll(context.Background(), server.URL+"/api/v1/agent/enroll", roots,
		token, "1.0.0", testNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.NodeID != testNodeID || len(credentials.CertPEM) == 0 || len(credentials.KeyPEM) == 0 || len(credentials.CAPEM) == 0 {
		t.Fatalf("incomplete credentials: %+v", credentials)
	}
	if _, err := tls.X509KeyPair(credentials.CertPEM, credentials.KeyPEM); err != nil {
		t.Fatalf("Agent key mismatch: %v", err)
	}
	directory := t.TempDir()
	certPath, keyPath := filepath.Join(directory, "agent.crt"), filepath.Join(directory, "agent.key")
	if err := SaveCredentials(certPath, keyPath, credentials); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{certPath, keyPath} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			t.Fatalf("credential permissions %s: %v, %v", path, info, err)
		}
	}
	if err := SaveCredentials(certPath, keyPath, credentials); err == nil {
		t.Fatal("existing credentials overwritten")
	}
	for _, path := range []string{certPath, keyPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("existing credential deleted: %s: %v", path, err)
		}
	}
	if _, err := Enroll(context.Background(), "http://"+server.Listener.Addr().String()+"/api/v1/agent/enroll", roots,
		token, "1.0.0", testNodeID); err == nil {
		t.Fatal("plaintext enrollment accepted")
	}
}
