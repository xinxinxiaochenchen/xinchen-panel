package agentidentity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net/url"
	"testing"
	"time"
)

func relayCSR(t *testing.T) []byte {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged, _ := url.Parse("spiffe://network-control-plane/relay/00000000-0000-0000-0000-000000000000")
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "untrusted"}, URIs: []*url.URL{forged}, DNSNames: []string{"forged.example.com"}}, private)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func TestIssueRelayServerCertificateBindsNodeAndAddress(t *testing.T) {
	caPEM, keyPEM := testCA(t)
	issuer, err := NewIssuer(caPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	issued, err := issuer.IssueRelayServerCertificate(relayCSR(t), certificateTestNodeID, "relay.example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(issued.CertificatePEM)
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	caBlock, _ := pem.Decode(caPEM)
	ca, _ := x509.ParseCertificate(caBlock.Bytes)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots, DNSName: "relay.example.com",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, CurrentTime: now}); err != nil {
		t.Fatalf("relay server certificate did not verify: %v", err)
	}
	if nodeID, err := CertificateRelayNodeID(certificate); err != nil || nodeID != certificateTestNodeID {
		t.Fatalf("relay node identity = %q, %v", nodeID, err)
	}
	if _, err := CertificateNodeID(certificate); err == nil {
		t.Fatal("relay server certificate accepted as control-plane Agent client")
	}
	if err := certificate.VerifyHostname("other.example.com"); err == nil {
		t.Fatal("relay certificate accepted another hostname")
	}
	if len(certificate.DNSNames) != 1 || certificate.DNSNames[0] != "relay.example.com" {
		t.Fatalf("CSR SAN leaked into signed certificate: %v", certificate.DNSNames)
	}
}

func TestIssueRelayServerCertificateRejectsInvalidAddress(t *testing.T) {
	caPEM, keyPEM := testCA(t)
	issuer, _ := NewIssuer(caPEM, keyPEM)
	for _, host := range []string{"", "*.example.com", "https://relay.example.com", "relay..example.com"} {
		if _, err := issuer.IssueRelayServerCertificate(relayCSR(t), certificateTestNodeID, host, time.Now()); err == nil {
			t.Fatalf("invalid relay address accepted: %q", host)
		}
	}
	issued, err := issuer.IssueRelayServerCertificate(relayCSR(t), certificateTestNodeID, "179.255.145.149", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(issued.CertificatePEM)
	certificate, _ := x509.ParseCertificate(block.Bytes)
	if err := certificate.VerifyHostname("179.255.145.149"); err != nil {
		t.Fatalf("IP SAN is missing: %v", err)
	}
}
