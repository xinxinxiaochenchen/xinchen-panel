package agentidentity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRelayCertificateGrantRequiresAuthorizedAgentAndNode(t *testing.T) {
	f := newEnrollmentFixture(t)
	ctx := context.Background()
	_, agentCSR := renewalKeyAndCSR(t)
	token, err := f.service.CreateToken(ctx, f.nodeID, f.actorID, "relay-grant")
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := f.service.Enroll(ctx, token.Token, agentCSR, "v1")
	if err != nil {
		t.Fatal(err)
	}
	agentCert := renewalCert(t, enrolled.CertificatePEM)
	_, relayCSR := renewalKeyAndCSR(t)
	if _, err = f.service.RelayCertificate(ctx, agentCert, relayCSR, ""); !errors.Is(err, ErrRelayUnavailable) {
		t.Fatalf("missing relay port = %v", err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE nodes SET relay_port=24443 WHERE id=$1`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	issued, err := f.service.RelayCertificate(ctx, agentCert, relayCSR, "")
	if err != nil {
		t.Fatal(err)
	}
	if issued.NodeID != f.nodeID || issued.Fingerprint == "" {
		t.Fatalf("bad grant: %+v", issued)
	}
	cert := renewalCert(t, issued.CertificatePEM)
	var host string
	if err = f.pool.QueryRow(ctx, `SELECT host FROM nodes WHERE id=$1`, f.nodeID).Scan(&host); err != nil {
		t.Fatal(err)
	}
	if err = cert.VerifyHostname(host); err != nil {
		t.Fatalf("wrong SAN: %v", err)
	}
	if got, err := CertificateRelayNodeID(cert); err != nil || got != f.nodeID {
		t.Fatalf("identity=%q %v", got, err)
	}
	retry, err := f.service.RelayCertificate(ctx, agentCert, relayCSR, "")
	if err != nil || string(retry.CertificatePEM) != string(issued.CertificatePEM) {
		t.Fatalf("retry=%v", err)
	}
	_, otherCSR := renewalKeyAndCSR(t)
	if _, err = f.service.RelayCertificate(ctx, agentCert, otherCSR, ""); !errors.Is(err, ErrRenewalTooEarly) {
		t.Fatalf("early rotation=%v", err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE agents SET status='revoked' WHERE node_id=$1`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.RelayCertificate(ctx, agentCert, relayCSR, ""); !errors.Is(err, ErrAgentUnauthorized) {
		t.Fatalf("revoked=%v", err)
	}
}

func TestRelayCertificateGrantRenewalAndReenrollment(t *testing.T) {
	f := newEnrollmentFixture(t)
	ctx := context.Background()
	_, agentCSR := renewalKeyAndCSR(t)
	token, err := f.service.CreateToken(ctx, f.nodeID, f.actorID, "relay-renew")
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := f.service.Enroll(ctx, token.Token, agentCSR, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `UPDATE nodes SET relay_port=24444 WHERE id=$1`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	agentCert := renewalCert(t, enrolled.CertificatePEM)
	_, relayCSR := renewalKeyAndCSR(t)
	first, err := f.service.RelayCertificate(ctx, agentCert, relayCSR, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.RelayCertificate(ctx, agentCert, relayCSR, first.Fingerprint); !errors.Is(err, ErrRenewalTooEarly) {
		t.Fatalf("early renewal=%v", err)
	}
	f.service.issuer.ca.NotBefore = time.Now().Add(-48 * time.Hour)
	var host string
	if err = f.pool.QueryRow(ctx, `SELECT host FROM nodes WHERE id=$1`, f.nodeID).Scan(&host); err != nil {
		t.Fatal(err)
	}
	short, err := f.service.issuer.IssueRelayServerCertificate(relayCSR, f.nodeID, host, time.Now().Add(-19*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `DELETE FROM agent_relay_certificate_grants WHERE node_id=$1`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO agent_relay_certificate_grants(node_id,fingerprint,csr_digest,certificate_pem,host,expires_at)
 VALUES($1,$2,repeat('a',64),$3,$4,$5)`, f.nodeID, short.Fingerprint, short.CertificatePEM, host, short.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	renewed, err := f.service.RelayCertificate(ctx, agentCert, relayCSR, short.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := f.service.RelayCertificate(ctx, agentCert, relayCSR, short.Fingerprint)
	if err != nil || string(retry.CertificatePEM) != string(renewed.CertificatePEM) {
		t.Fatalf("renew retry=%v", err)
	}
	var count int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM agent_relay_certificate_grants WHERE node_id=$1 AND expires_at>clock_timestamp()`, f.nodeID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("overlap count=%d %v", count, err)
	}
	token, err = f.service.CreateToken(ctx, f.nodeID, f.actorID, "relay-reenroll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Enroll(ctx, token.Token, testCSR(t), "v2"); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM agent_relay_certificate_grants WHERE node_id=$1`, f.nodeID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("grants survived reenroll=%d %v", count, err)
	}
}

func TestRelayCertificateGrantsInvalidateOnResourceChanges(t *testing.T) {
	for _, change := range []struct{ name, sql string }{
		{"host", `UPDATE nodes SET host=id::text || '.changed.example.org' WHERE id=$1`},
		{"port", `UPDATE nodes SET relay_port=24445 WHERE id=$1`},
		{"disable node", `UPDATE nodes SET enabled=false WHERE id=$1`},
		{"disable group", `UPDATE resource_groups SET enabled=false WHERE id=(SELECT group_id FROM nodes WHERE id=$1)`},
		{"revoke Agent", `UPDATE agents SET status='revoked' WHERE node_id=$1`},
	} {
		t.Run(change.name, func(t *testing.T) {
			f := newEnrollmentFixture(t)
			ctx := context.Background()
			_, agentCSR := renewalKeyAndCSR(t)
			token, err := f.service.CreateToken(ctx, f.nodeID, f.actorID, "relay-change")
			if err != nil {
				t.Fatal(err)
			}
			enrolled, err := f.service.Enroll(ctx, token.Token, agentCSR, "v1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.pool.Exec(ctx, `UPDATE nodes SET relay_port=24443 WHERE id=$1`, f.nodeID); err != nil {
				t.Fatal(err)
			}
			_, relayCSR := renewalKeyAndCSR(t)
			if _, err = f.service.RelayCertificate(ctx, renewalCert(t, enrolled.CertificatePEM), relayCSR, ""); err != nil {
				t.Fatal(err)
			}
			if _, err = f.pool.Exec(ctx, change.sql, f.nodeID); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM agent_relay_certificate_grants WHERE node_id=$1`, f.nodeID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("old grant survived %s: %d %v", change.name, count, err)
			}
		})
	}
}
