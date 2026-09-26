package agentidentity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const CertificateRenewalWindow = 6 * time.Hour

var ErrRenewalTooEarly = errors.New("Agent certificate renewal is too early")
var ErrRenewalGrantLimit = errors.New("Agent certificate renewal grant limit reached")

// Renew authorizes the currently presented certificate under the node lock,
// signs a same-key CSR and persists the new grant before returning its PEM.
// A retry with the same parent certificate returns the same response.
func (service *EnrollmentService) Renew(ctx context.Context, current *x509.Certificate, csrPEM []byte, version string) (EnrollmentResult, error) {
	if current == nil || len(csrPEM) == 0 || len(csrPEM) > 16<<10 || !agentVersionPattern.MatchString(version) {
		return EnrollmentResult{}, ErrInvalidCSR
	}
	nodeID, err := CertificateNodeID(current)
	if err != nil {
		return EnrollmentResult{}, ErrAgentUnauthorized
	}
	now := time.Now()
	if now.Before(current.NotBefore) || !now.Before(current.NotAfter) {
		return EnrollmentResult{}, ErrAgentUnauthorized
	}
	roots := x509.NewCertPool()
	roots.AddCert(service.issuer.ca)
	if _, err := current.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return EnrollmentResult{}, ErrAgentUnauthorized
	}
	block, rest := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return EnrollmentResult{}, ErrInvalidCSR
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || request.CheckSignature() != nil {
		return EnrollmentResult{}, ErrInvalidCSR
	}
	requestedKey, ok := request.PublicKey.(ed25519.PublicKey)
	currentKey, currentOK := current.PublicKey.(ed25519.PublicKey)
	if !ok || !currentOK || !bytes.Equal(requestedKey, currentKey) {
		return EnrollmentResult{}, ErrInvalidCSR
	}
	currentFingerprint := sha256.Sum256(current.Raw)
	parent := hex.EncodeToString(currentFingerprint[:])
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("begin Agent renewal: %w", err)
	}
	defer tx.Rollback(ctx)
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT enabled FROM nodes WHERE id=$1 FOR UPDATE`, nodeID).Scan(&enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EnrollmentResult{}, ErrAgentUnauthorized
		}
		return EnrollmentResult{}, fmt.Errorf("lock renewal node: %w", err)
	}
	if !enabled {
		return EnrollmentResult{}, ErrAgentUnauthorized
	}
	var authorized bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents a WHERE a.node_id=$1 AND a.status <> 'revoked'
	AND ((a.cert_fingerprint=$2 AND a.cert_expires_at > clock_timestamp()) OR EXISTS
	(SELECT 1 FROM agent_certificate_grants g WHERE g.node_id=a.node_id AND g.fingerprint=$2 AND g.expires_at > clock_timestamp())))`, nodeID, parent).Scan(&authorized); err != nil {
		return EnrollmentResult{}, fmt.Errorf("authorize renewal certificate: %w", err)
	}
	if !authorized {
		return EnrollmentResult{}, ErrAgentUnauthorized
	}
	if current.NotAfter.Sub(now) > CertificateRenewalWindow {
		return EnrollmentResult{}, ErrRenewalTooEarly
	}
	var existingPEM []byte
	var existingFingerprint string
	var existingExpiry time.Time
	err = tx.QueryRow(ctx, `SELECT certificate_pem,fingerprint,expires_at FROM agent_certificate_grants
	WHERE node_id=$1 AND parent_fingerprint=$2 AND expires_at > clock_timestamp()`, nodeID, parent).Scan(&existingPEM, &existingFingerprint, &existingExpiry)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return EnrollmentResult{}, fmt.Errorf("read Agent renewal retry: %w", err)
	}
	if err == nil {
		return EnrollmentResult{NodeID: nodeID, CertificatePEM: existingPEM,
			CACertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: service.issuer.ca.Raw}),
			Fingerprint:      existingFingerprint, ExpiresAt: existingExpiry}, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_certificate_grants WHERE node_id=$1 AND expires_at <= clock_timestamp()`, nodeID); err != nil {
		return EnrollmentResult{}, fmt.Errorf("clear expired Agent grants: %w", err)
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM agent_certificate_grants WHERE node_id=$1`, nodeID).Scan(&active); err != nil {
		return EnrollmentResult{}, fmt.Errorf("count Agent grants: %w", err)
	}
	if active >= 3 {
		return EnrollmentResult{}, ErrRenewalGrantLimit
	}
	issued, err := service.issuer.IssueClientCertificate(csrPEM, nodeID, now)
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("%w: %v", ErrInvalidCSR, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO agent_certificate_grants(node_id,fingerprint,parent_fingerprint,certificate_pem,expires_at)
	VALUES ($1,$2,$3,$4,$5)`, nodeID, issued.Fingerprint, parent, issued.CertificatePEM, issued.ExpiresAt); err != nil {
		return EnrollmentResult{}, fmt.Errorf("store Agent renewal: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET version=$2 WHERE node_id=$1`, nodeID, version); err != nil {
		return EnrollmentResult{}, fmt.Errorf("update Agent version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EnrollmentResult{}, fmt.Errorf("commit Agent renewal: %w", err)
	}
	return EnrollmentResult{NodeID: nodeID, CertificatePEM: issued.CertificatePEM,
		CACertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: service.issuer.ca.Raw}),
		Fingerprint:      issued.Fingerprint, ExpiresAt: issued.ExpiresAt}, nil
}

func (service *EnrollmentService) SweepExpiredGrants(ctx context.Context) (int64, error) {
	tag, err := service.pool.Exec(ctx, `DELETE FROM agent_certificate_grants WHERE expires_at <= clock_timestamp()`)
	if err != nil {
		return 0, fmt.Errorf("sweep expired Agent grants: %w", err)
	}
	return tag.RowsAffected(), nil
}
