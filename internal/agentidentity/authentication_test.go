package agentidentity

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"testing"
	"time"
)

func TestCertificateNodeIDRejectsWrongIdentity(t *testing.T) {
	caPEM, keyPEM := testCA(t)
	issuer, err := NewIssuer(caPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issuer.IssueClientCertificate(testCSR(t), certificateTestNodeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(issued.CertificatePEM)
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if nodeID, err := CertificateNodeID(certificate); err != nil || nodeID != certificateTestNodeID {
		t.Fatalf("certificate node ID = %q, %v", nodeID, err)
	}
	certificate.URIs = nil
	if _, err := CertificateNodeID(certificate); err == nil {
		t.Fatal("certificate without Agent URI accepted")
	}
}

func TestAuthenticateCertificateRejectsRevokedAndOldFingerprints(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	ctx := context.Background()
	token, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "test-request")
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.Enroll(ctx, token.Token, testCSR(t), "v1.0")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(result.CertificatePEM)
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if nodeID, err := fixture.service.AuthenticateCertificate(ctx, certificate); err != nil || nodeID != fixture.nodeID {
		t.Fatalf("valid certificate = %q, %v", nodeID, err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE agents SET cert_fingerprint=$2 WHERE node_id=$1`, fixture.nodeID, hex.EncodeToString(sha256.New().Sum(nil))); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.AuthenticateCertificate(ctx, certificate); !errors.Is(err, ErrAgentUnauthorized) {
		t.Fatalf("old fingerprint = %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE agents SET cert_fingerprint=$2,status='revoked' WHERE node_id=$1`, fixture.nodeID, result.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.AuthenticateCertificate(ctx, certificate); !errors.Is(err, ErrAgentUnauthorized) {
		t.Fatalf("revoked Agent = %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE agents SET status='pending',cert_expires_at=now()-interval '1 second' WHERE node_id=$1`, fixture.nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.AuthenticateCertificate(ctx, certificate); !errors.Is(err, ErrAgentUnauthorized) {
		t.Fatalf("database-expired certificate = %v", err)
	}
}
