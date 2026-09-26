package agentidentity

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"testing"
	"time"
)

const certificateTestNodeID = "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"

func testCA(t *testing.T) ([]byte, []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Agent CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
}

func testCSR(t *testing.T) []byte {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	maliciousURI, _ := url.Parse("spiffe://network-control-plane/agent/00000000-0000-0000-0000-000000000000")
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "other-node"}, URIs: []*url.URL{maliciousURI}}, private)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func TestIssueClientCertificateBindsNodeAndRejectsCSRIdentity(t *testing.T) {
	caPEM, keyPEM := testCA(t)
	issuer, err := NewIssuer(caPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	issued, err := issuer.IssueClientCertificate(testCSR(t), certificateTestNodeID, now)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(issued.CertificatePEM)
	if block == nil {
		t.Fatal("missing certificate PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	caBlock, _ := pem.Decode(caPEM)
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: now}); err != nil {
		t.Fatalf("issued certificate does not verify: %v", err)
	}
	if len(certificate.URIs) != 1 || certificate.URIs[0].String() != "spiffe://network-control-plane/agent/"+certificateTestNodeID {
		t.Fatalf("certificate identity = %v", certificate.URIs)
	}
	if certificate.Subject.CommonName != certificateTestNodeID || len(certificate.ExtKeyUsage) != 1 || certificate.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("unexpected certificate identity or EKU: %+v", certificate)
	}
	if !certificate.NotAfter.Equal(now.Add(24*time.Hour)) || issued.Fingerprint == "" {
		t.Fatalf("expiry or fingerprint wrong: %s %q", certificate.NotAfter, issued.Fingerprint)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, CurrentTime: now}); err == nil {
		t.Fatal("client certificate verified as server")
	}
}

func TestIssuerRejectsInvalidCAAndCSR(t *testing.T) {
	caPEM, keyPEM := testCA(t)
	otherCA, _ := testCA(t)
	if _, err := NewIssuer(otherCA, keyPEM); err == nil {
		t.Fatal("mismatched CA key accepted")
	}
	issuer, err := NewIssuer(caPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.IssueClientCertificate([]byte("not a CSR"), certificateTestNodeID, time.Now()); err == nil {
		t.Fatal("invalid CSR accepted")
	}
	if _, err := issuer.IssueClientCertificate(testCSR(t), "other-node", time.Now()); err == nil {
		t.Fatal("invalid node ID accepted")
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaCSR, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, ecdsaKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.IssueClientCertificate(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: ecdsaCSR}), certificateTestNodeID, time.Now()); err == nil {
		t.Fatal("non-Ed25519 CSR accepted")
	}
}
