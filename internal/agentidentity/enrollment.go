package agentidentity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"time"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidEnrollment = errors.New("invalid or expired Agent enrollment")
	ErrNodeNotFound      = errors.New("Agent node not found or disabled")
	ErrInvalidCSR        = errors.New("invalid Agent certificate request")
)

var agentVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

type EnrollmentToken struct {
	Token     string
	ExpiresAt time.Time
}

type EnrollmentResult struct {
	NodeID           string
	CertificatePEM   []byte
	CACertificatePEM []byte
	Fingerprint      string
	ExpiresAt        time.Time
}

type EnrollmentService struct {
	pool   *pgxpool.Pool
	issuer *Issuer
}

func NewEnrollmentService(pool *pgxpool.Pool, issuer *Issuer) *EnrollmentService {
	return &EnrollmentService{pool: pool, issuer: issuer}
}

// CreateToken revokes any unused token for this node. Only the caller receives
// the newly generated bearer token; PostgreSQL stores its SHA-256 digest.
func (service *EnrollmentService) CreateToken(ctx context.Context, nodeID, actorID, requestID string) (EnrollmentToken, error) {
	if !nodeIDPattern.MatchString(nodeID) || !nodeIDPattern.MatchString(actorID) {
		return EnrollmentToken{}, ErrNodeNotFound
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return EnrollmentToken{}, fmt.Errorf("generate Agent enrollment token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(token))
	tokenID, err := id.NewV7()
	if err != nil {
		return EnrollmentToken{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return EnrollmentToken{}, fmt.Errorf("begin Agent token creation: %w", err)
	}
	defer tx.Rollback(ctx)
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT enabled FROM nodes WHERE id=$1 FOR UPDATE`, nodeID).Scan(&enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EnrollmentToken{}, ErrNodeNotFound
		}
		return EnrollmentToken{}, fmt.Errorf("lock Agent node: %w", err)
	}
	if !enabled {
		return EnrollmentToken{}, ErrNodeNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_enrollment_tokens WHERE node_id=$1 AND consumed_at IS NULL`, nodeID); err != nil {
		return EnrollmentToken{}, fmt.Errorf("replace Agent token: %w", err)
	}
	var expiresAt time.Time
	if err := tx.QueryRow(ctx, `INSERT INTO agent_enrollment_tokens(id,node_id,token_hash,created_by,expires_at)
VALUES ($1,$2,$3,$4,now()+interval '10 minutes') RETURNING expires_at`, tokenID, nodeID, hash[:], actorID).Scan(&expiresAt); err != nil {
		return EnrollmentToken{}, fmt.Errorf("store Agent token digest: %w", err)
	}
	auditID, err := id.NewV7()
	if err != nil {
		return EnrollmentToken{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,'create','agent_enrollment',$3,'{"status":"issued"}'::jsonb,$4)`, auditID, actorID, nodeID, requestID); err != nil {
		return EnrollmentToken{}, fmt.Errorf("audit Agent token creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EnrollmentToken{}, fmt.Errorf("commit Agent token: %w", err)
	}
	return EnrollmentToken{Token: token, ExpiresAt: expiresAt}, nil
}

// Enroll locks the node before the token so replacement and consumption have
// the same lock order. Signing and digest persistence complete in one transaction.
func (service *EnrollmentService) Enroll(ctx context.Context, token string, csrPEM []byte, version string) (EnrollmentResult, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || len(csrPEM) == 0 || len(csrPEM) > 16<<10 || !agentVersionPattern.MatchString(version) {
		return EnrollmentResult{}, ErrInvalidEnrollment
	}
	hash := sha256.Sum256([]byte(token))
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("begin Agent enrollment: %w", err)
	}
	defer tx.Rollback(ctx)
	var nodeID string
	if err := tx.QueryRow(ctx, `SELECT node_id::text FROM agent_enrollment_tokens WHERE token_hash=$1`, hash[:]).Scan(&nodeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EnrollmentResult{}, ErrInvalidEnrollment
		}
		return EnrollmentResult{}, fmt.Errorf("find Agent enrollment token: %w", err)
	}
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT enabled FROM nodes WHERE id=$1 FOR UPDATE`, nodeID).Scan(&enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EnrollmentResult{}, ErrInvalidEnrollment
		}
		return EnrollmentResult{}, fmt.Errorf("lock Agent enrollment node: %w", err)
	}
	if !enabled {
		return EnrollmentResult{}, ErrInvalidEnrollment
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT consumed_at IS NULL AND expires_at > clock_timestamp()
FROM agent_enrollment_tokens WHERE token_hash=$1 AND node_id=$2 FOR UPDATE`, hash[:], nodeID).Scan(&valid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EnrollmentResult{}, ErrInvalidEnrollment
		}
		return EnrollmentResult{}, fmt.Errorf("lock Agent enrollment token: %w", err)
	}
	if !valid {
		return EnrollmentResult{}, ErrInvalidEnrollment
	}
	issued, err := service.issuer.IssueClientCertificate(csrPEM, nodeID, time.Now())
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("%w: %v", ErrInvalidCSR, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO agents(id,node_id,cert_fingerprint,cert_expires_at,version,status)
VALUES (gen_random_uuid(),$1,$2,$3,$4,'pending')
ON CONFLICT (node_id) DO UPDATE SET cert_fingerprint=EXCLUDED.cert_fingerprint,
cert_expires_at=EXCLUDED.cert_expires_at,version=EXCLUDED.version,status='pending',
last_seen_at=NULL,applied_revision=0`, nodeID, issued.Fingerprint, issued.ExpiresAt, version); err != nil {
		return EnrollmentResult{}, fmt.Errorf("store Agent certificate identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_metrics WHERE node_id=$1`, nodeID); err != nil {
		return EnrollmentResult{}, fmt.Errorf("clear previous Agent metrics: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_certificate_grants WHERE node_id=$1`, nodeID); err != nil {
		return EnrollmentResult{}, fmt.Errorf("revoke prior Agent renewal grants: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_enrollment_tokens SET consumed_at=now() WHERE token_hash=$1`, hash[:]); err != nil {
		return EnrollmentResult{}, fmt.Errorf("consume Agent enrollment token: %w", err)
	}
	var stillValid bool
	if err := tx.QueryRow(ctx, `SELECT expires_at > clock_timestamp() FROM agent_enrollment_tokens WHERE token_hash=$1`, hash[:]).Scan(&stillValid); err != nil {
		return EnrollmentResult{}, fmt.Errorf("recheck Agent enrollment expiry: %w", err)
	}
	if !stillValid {
		return EnrollmentResult{}, ErrInvalidEnrollment
	}
	if err := tx.Commit(ctx); err != nil {
		return EnrollmentResult{}, fmt.Errorf("commit Agent enrollment: %w", err)
	}
	return EnrollmentResult{NodeID: nodeID, CertificatePEM: issued.CertificatePEM,
		CACertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: service.issuer.ca.Raw}),
		Fingerprint:      issued.Fingerprint, ExpiresAt: issued.ExpiresAt}, nil
}
