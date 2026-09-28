package agentidentity

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func enrollmentTestDatabaseURL() string {
	if value := os.Getenv("CONTROL_TEST_DATABASE_URL"); value != "" {
		return value
	}
	if os.Getenv("CONTROL_TEST_DB_NAME") != "" && os.Getenv("POSTGRES_PASSWORD") != "" {
		return (&url.URL{Scheme: "postgres", User: url.UserPassword("controlplane", os.Getenv("POSTGRES_PASSWORD")),
			Host: "db:5432", Path: "/" + os.Getenv("CONTROL_TEST_DB_NAME")}).String()
	}
	return ""
}

type enrollmentFixture struct {
	pool    *pgxpool.Pool
	service *EnrollmentService
	nodeID  string
	actorID string
}

func newEnrollmentFixture(t *testing.T) *enrollmentFixture {
	t.Helper()
	url := enrollmentTestDatabaseURL()
	if url == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	caPEM, keyPEM := testCA(t)
	issuer, err := NewIssuer(caPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &enrollmentFixture{pool: pool, service: NewEnrollmentService(pool, issuer)}
	var groupID string
	if err := pool.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region)
VALUES (gen_random_uuid(),gen_random_uuid()::text,'Enrollment','US') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,capabilities)
VALUES (gen_random_uuid(),$1,'Enrollment node','US',gen_random_uuid()::text || '.example.org',ARRAY['forward']) RETURNING id::text`, groupID).Scan(&fixture.nodeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status)
VALUES (gen_random_uuid(),gen_random_uuid()::text || '@example.invalid','hash','active') RETURNING id::text`).Scan(&fixture.actorID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestEnrollmentConsumesTokenAndPersistsCertificateIdentity(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	ctx := context.Background()
	issuedToken, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "enrollment-request")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(issuedToken.Token)
	if err != nil || len(decoded) != 32 || issuedToken.ExpiresAt.Before(time.Now().Add(9*time.Minute)) {
		t.Fatalf("token entropy or lifetime invalid: %v, %s", err, issuedToken.ExpiresAt)
	}
	var storedHash []byte
	if err := fixture.pool.QueryRow(ctx, `SELECT token_hash FROM agent_enrollment_tokens WHERE node_id=$1`, fixture.nodeID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(issuedToken.Token))
	if string(storedHash) != string(sum[:]) {
		t.Fatal("stored token is not its SHA-256 digest")
	}
	var auditCount int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_user_id=$1
AND object_type='agent_enrollment' AND object_id=$2 AND request_id='enrollment-request'
AND after_json::text NOT LIKE '%' || $3 || '%'`, fixture.actorID, fixture.nodeID, issuedToken.Token).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("token creation audit = %d, %v", auditCount, err)
	}
	if _, err := fixture.service.Enroll(ctx, issuedToken.Token, []byte("broken CSR"), "v1.0"); err == nil {
		t.Fatal("malformed CSR accepted")
	}
	result, err := fixture.service.Enroll(ctx, issuedToken.Token, testCSR(t), "v1.0")
	if err != nil {
		t.Fatal(err)
	}
	if result.NodeID != fixture.nodeID || len(result.CertificatePEM) == 0 || len(result.CACertificatePEM) == 0 || result.Fingerprint == "" {
		t.Fatalf("incomplete enrollment result: %+v", result)
	}
	var fingerprint, version, status string
	var expiresAt time.Time
	if err := fixture.pool.QueryRow(ctx, `SELECT cert_fingerprint,version,status,cert_expires_at FROM agents WHERE node_id=$1`, fixture.nodeID).
		Scan(&fingerprint, &version, &status, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if fingerprint != result.Fingerprint || version != "v1.0" || status != "pending" || !expiresAt.Equal(result.ExpiresAt) {
		t.Fatalf("Agent identity not stored: %s %s %s %s", fingerprint, version, status, expiresAt)
	}
	if _, err := fixture.service.Enroll(ctx, issuedToken.Token, testCSR(t), "v1.0"); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("replay = %v", err)
	}
	pendingToken, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "pending-revocation")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.RevokeAgent(ctx, fixture.nodeID, fixture.actorID, "revoke-agent"); err != nil {
		t.Fatalf("revoke Agent: %v", err)
	}
	if _, err := fixture.service.Enroll(ctx, pendingToken.Token, testCSR(t), "v1.0"); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("unused token survived revocation: %v", err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT status FROM agents WHERE node_id=$1`, fixture.nodeID).Scan(&status); err != nil || status != "revoked" {
		t.Fatalf("revoked Agent status = %q, %v", status, err)
	}
	var grants int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM agent_certificate_grants WHERE node_id=$1`, fixture.nodeID).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("renewal grants survived explicit revoke: %d, %v", grants, err)
	}
	block, _ := pem.Decode(result.CertificatePEM)
	if block == nil {
		t.Fatal("enrolled certificate is not PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.AuthenticateCertificate(ctx, certificate); !errors.Is(err, ErrAgentUnauthorized) {
		t.Fatalf("revoked certificate accepted: %v", err)
	}
	newToken, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "reenroll-after-revoke")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Enroll(ctx, newToken.Token, testCSR(t), "v2.0"); err != nil {
		t.Fatalf("reenrollment after revocation: %v", err)
	}
}

func TestReenrollmentClearsPreviousAgentMetrics(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	ctx := context.Background()
	first, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "metrics-first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Enroll(ctx, first.Token, testCSR(t), "v1.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `INSERT INTO agent_metrics(node_id,uptime_seconds,cpu_pct,memory_used_bytes,rx_bytes,tx_bytes,connections,engine_status)
VALUES ($1,100,25,1024,2048,4096,3,'running')`, fixture.nodeID); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "metrics-second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Enroll(ctx, second.Token, testCSR(t), "v2.0"); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM agent_metrics WHERE node_id=$1`, fixture.nodeID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("old Agent metrics survived reenrollment: %d", remaining)
	}
}

func TestEnrollmentReplacesOldTokenAndRejectsExpiredOrDisabledNode(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	ctx := context.Background()
	first, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "test-request")
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "test-request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Enroll(ctx, first.Token, testCSR(t), "v1.0"); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("replaced token = %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE agent_enrollment_tokens SET created_at=now()-interval '20 minutes',
expires_at=now()-interval '1 second' WHERE node_id=$1`, fixture.nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Enroll(ctx, second.Token, testCSR(t), "v1.0"); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("expired token = %v", err)
	}
	third, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "test-request")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE nodes SET enabled=false WHERE id=$1`, fixture.nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Enroll(ctx, third.Token, testCSR(t), "v1.0"); !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("disabled node enrollment = %v", err)
	}
}

func TestEnrollmentConcurrentReplayHasOneWinner(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	ctx := context.Background()
	token, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "test-request")
	if err != nil {
		t.Fatal(err)
	}
	csr := testCSR(t)
	var results [2]error
	var group sync.WaitGroup
	for index := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			_, results[index] = fixture.service.Enroll(ctx, token.Token, csr, "v1.0")
		}()
	}
	group.Wait()
	successes, replays := 0, 0
	for _, err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrInvalidEnrollment) {
			replays++
		} else {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("concurrent outcomes = %v", results)
	}
}

func TestEnrollmentRejectsTokenThatExpiresWhileWaitingForNodeLock(t *testing.T) {
	fixture := newEnrollmentFixture(t)
	ctx := context.Background()
	token, err := fixture.service.CreateToken(ctx, fixture.nodeID, fixture.actorID, "expiry-lock-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE agent_enrollment_tokens SET expires_at=clock_timestamp()+interval '800 milliseconds' WHERE node_id=$1`, fixture.nodeID); err != nil {
		t.Fatal(err)
	}
	blocker, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `SELECT id FROM nodes WHERE id=$1 FOR UPDATE`, fixture.nodeID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	csr := testCSR(t)
	go func() { _, err := fixture.service.Enroll(ctx, token.Token, csr, "v1.0"); result <- err }()
	deadline := time.Now().Add(600 * time.Millisecond)
	for {
		var blocked int
		if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT enabled FROM nodes WHERE id=% FOR UPDATE'`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("enrollment did not reach node lock before expiry")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(850 * time.Millisecond)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrInvalidEnrollment) {
		t.Fatalf("expired during lock wait = %v", err)
	}
}
