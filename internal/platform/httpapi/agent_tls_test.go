package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"controlplane/internal/platform/config"
)

func TestLoadAgentTLSUsesTLS13AndConfiguredClientCA(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Agent test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, public, private)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, public, private)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	write := func(name string, content []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cfg := config.Config{AgentTLSAddr: "127.0.0.1:18443",
		AgentTLSCertFile: write("server.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})),
		AgentTLSKeyFile:  write("server.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
		AgentCACertFile:  write("ca.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
	}
	loaded, err := LoadAgentTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MinVersion != tls.VersionTLS13 || loaded.ClientAuth != tls.VerifyClientCertIfGiven || len(loaded.Certificates) != 1 ||
		len(loaded.ClientCAs.Subjects()) != 1 {
		t.Fatalf("unexpected Agent TLS configuration: %+v", loaded)
	}
	if _, err := LoadAgentTLS(config.Config{}); err == nil {
		t.Fatal("missing Agent TLS listener accepted")
	}
}
