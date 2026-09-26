package agentidentity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"testing"
	"time"
)

func renewalKeyAndCSR(t *testing.T) (ed25519.PrivateKey, []byte) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func renewalCert(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestCertificateRenewalRetainsOverlapAndRevokesOnReenrollment(t *testing.T) {
	f := newEnrollmentFixture(t)
	ctx := context.Background()
	_, csr := renewalKeyAndCSR(t)
	token, err := f.service.CreateToken(ctx, f.nodeID, f.actorID, "renewal-test")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := f.service.Enroll(ctx, token.Token, csr, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Renew(ctx, renewalCert(t, initial.CertificatePEM), csr, "v1"); !errors.Is(err, ErrRenewalTooEarly) {
		t.Fatalf("early renewal = %v", err)
	}
	f.service.issuer.ca.NotBefore = time.Now().Add(-48 * time.Hour)
	short, err := f.service.issuer.IssueClientCertificate(csr, f.nodeID, time.Now().Add(-19*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	old := renewalCert(t, short.CertificatePEM)
	if _, err := f.pool.Exec(ctx, `UPDATE agents SET cert_fingerprint=$2,cert_expires_at=$3 WHERE node_id=$1`, f.nodeID, short.Fingerprint, short.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	renewed, err := f.service.Renew(ctx, old, csr, "v1")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := f.service.Renew(ctx, old, csr, "v1")
	if err != nil || string(retry.CertificatePEM) != string(renewed.CertificatePEM) {
		t.Fatalf("idempotent retry = %v", err)
	}
	newCert := renewalCert(t, renewed.CertificatePEM)
	if _, err := f.service.AuthenticateCertificate(ctx, old); err != nil {
		t.Fatalf("old overlap: %v", err)
	}
	if _, err := f.service.AuthenticateCertificate(ctx, newCert); err != nil {
		t.Fatalf("new grant: %v", err)
	}
	_, otherCSR := renewalKeyAndCSR(t)
	if _, err := f.service.Renew(ctx, old, otherCSR, "v1"); !errors.Is(err, ErrInvalidCSR) {
		t.Fatalf("other key: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE agents SET status='revoked' WHERE node_id=$1`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AuthenticateCertificate(ctx, newCert); !errors.Is(err, ErrAgentUnauthorized) {
		t.Fatalf("revoked grant: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE agents SET status='pending' WHERE node_id=$1`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	token, err = f.service.CreateToken(ctx, f.nodeID, f.actorID, "reenroll-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Enroll(ctx, token.Token, testCSR(t), "v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AuthenticateCertificate(ctx, newCert); !errors.Is(err, ErrAgentUnauthorized) {
		t.Fatalf("reenrolled grant: %v", err)
	}
}

func TestCertificateRenewalRejectsExpiredOrUnauthorized(t *testing.T) {
	f := newEnrollmentFixture(t)
	ctx := context.Background()
	_, csr := renewalKeyAndCSR(t)
	token, _ := f.service.CreateToken(ctx, f.nodeID, f.actorID, "renewal-expiry")
	initial, err := f.service.Enroll(ctx, token.Token, csr, "v1")
	if err != nil {
		t.Fatal(err)
	}
	cert := renewalCert(t, initial.CertificatePEM)
	if _, err := f.pool.Exec(ctx, `UPDATE agents SET cert_expires_at=now()-interval '1 second' WHERE node_id=$1`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Renew(ctx, cert, csr, "v1"); !errors.Is(err, ErrAgentUnauthorized) {
		t.Fatalf("expired authorization: %v", err)
	}
	if _, err := f.service.Renew(ctx, cert, csr, "bad version with space"); !errors.Is(err, ErrInvalidCSR) {
		t.Fatalf("invalid version: %v", err)
	}
	if _, err := f.service.Renew(ctx, cert, make([]byte, 16<<10+1), "v1"); !errors.Is(err, ErrInvalidCSR) {
		t.Fatalf("oversize CSR: %v", err)
	}
}

func TestExpiredCertificateGrantsAreSwept(t *testing.T) {
	f := newEnrollmentFixture(t)
	ctx := context.Background()
	token, err := f.service.CreateToken(ctx, f.nodeID, f.actorID, "renewal-sweep")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Enroll(ctx, token.Token, testCSR(t), "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO agent_certificate_grants(node_id,fingerprint,parent_fingerprint,certificate_pem,expires_at)
VALUES($1,repeat('a',64),repeat('b',64),'expired',now()-interval '1 second')`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	count, err := f.service.SweepExpiredGrants(ctx)
	if err != nil || count != 1 {
		t.Fatalf("swept %d grants: %v", count, err)
	}
}
