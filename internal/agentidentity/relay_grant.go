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
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrRelayUnavailable = errors.New("node relay is unavailable")
var relayFingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// RelayCertificate issues a separate server identity. All resource and Agent
// authorization checks run under row locks, including the database revocation
// check. The caller cannot select a node or a SAN. Renewal retains the relay key.
func (service *EnrollmentService) RelayCertificate(ctx context.Context, current *x509.Certificate, csrPEM []byte, parent string) (EnrollmentResult, error) {
	nodeID, err := CertificateNodeID(current)
	if err != nil {
		return EnrollmentResult{}, ErrAgentUnauthorized
	}
	roots := x509.NewCertPool()
	roots.AddCert(service.issuer.ca)
	if _, err = current.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return EnrollmentResult{}, ErrAgentUnauthorized
	}
	if len(csrPEM) == 0 || len(csrPEM) > 16<<10 || (parent != "" && !relayFingerprintPattern.MatchString(parent)) {
		return EnrollmentResult{}, ErrInvalidCSR
	}
	block, rest := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return EnrollmentResult{}, ErrInvalidCSR
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || request.CheckSignature() != nil {
		return EnrollmentResult{}, ErrInvalidCSR
	}
	key, ok := request.PublicKey.(ed25519.PublicKey)
	if !ok || bytes.Equal(key, current.PublicKey.(ed25519.PublicKey)) {
		return EnrollmentResult{}, ErrInvalidCSR
	}
	digest := sha256.Sum256(block.Bytes)
	csrDigest := hex.EncodeToString(digest[:])
	fingerprint := sha256.Sum256(current.Raw)
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("begin relay certificate: %w", err)
	}
	defer tx.Rollback(ctx)
	var host, groupID string
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT host,group_id::text,enabled AND relay_port IS NOT NULL AND 'forward'=ANY(capabilities)
 FROM nodes WHERE id=$1 FOR UPDATE`, nodeID).Scan(&host, &groupID, &eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnrollmentResult{}, ErrAgentUnauthorized
	}
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("lock relay node: %w", err)
	}
	var groupEnabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM resource_groups WHERE id=$1 FOR SHARE`, groupID).Scan(&groupEnabled); err != nil {
		return EnrollmentResult{}, fmt.Errorf("lock relay group: %w", err)
	}
	var authorized bool
	err = tx.QueryRow(ctx, `SELECT a.status<>'revoked' AND ((a.cert_fingerprint=$2 AND a.cert_expires_at>clock_timestamp()) OR EXISTS
 (SELECT 1 FROM agent_certificate_grants g WHERE g.node_id=a.node_id AND g.fingerprint=$2 AND g.expires_at>clock_timestamp()))
 FROM agents a WHERE a.node_id=$1 FOR UPDATE`, nodeID, hex.EncodeToString(fingerprint[:])).Scan(&authorized)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnrollmentResult{}, ErrAgentUnauthorized
	}
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("lock relay Agent: %w", err)
	}
	// Recheck wall-clock expiry after waiting for the locks.
	now := time.Now()
	if !authorized || now.Before(current.NotBefore) || !now.Before(current.NotAfter) {
		return EnrollmentResult{}, ErrAgentUnauthorized
	}
	if !eligible || !groupEnabled || !validRelayHost(host) {
		return EnrollmentResult{}, ErrRelayUnavailable
	}
	if _, err = tx.Exec(ctx, `DELETE FROM agent_relay_certificate_grants WHERE node_id=$1 AND expires_at<=clock_timestamp()`, nodeID); err != nil {
		return EnrollmentResult{}, fmt.Errorf("expire relay grants: %w", err)
	}
	var existing EnrollmentResult
	var existingDigest string
	query := `SELECT certificate_pem,fingerprint,expires_at,csr_digest FROM agent_relay_certificate_grants WHERE node_id=$1 AND host=$2`
	args := []any{nodeID, host}
	if parent != "" {
		query += ` AND parent_fingerprint=$3`
		args = append(args, parent)
	} else {
		query += ` ORDER BY expires_at DESC LIMIT 1`
	}
	err = tx.QueryRow(ctx, query, args...).Scan(&existing.CertificatePEM, &existing.Fingerprint, &existing.ExpiresAt, &existingDigest)
	if err == nil {
		if existingDigest != csrDigest {
			return EnrollmentResult{}, ErrRenewalTooEarly
		}
		existing.NodeID = nodeID
		existing.CACertificatePEM = service.relayCAPEM()
		if err = tx.Commit(ctx); err != nil {
			return EnrollmentResult{}, fmt.Errorf("commit relay retry: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return EnrollmentResult{}, fmt.Errorf("read relay retry: %w", err)
	}
	if parent != "" {
		var priorPEM []byte
		var expiry time.Time
		err = tx.QueryRow(ctx, `SELECT certificate_pem,expires_at FROM agent_relay_certificate_grants WHERE node_id=$1 AND fingerprint=$2 AND host=$3`, nodeID, parent, host).Scan(&priorPEM, &expiry)
		if errors.Is(err, pgx.ErrNoRows) {
			return EnrollmentResult{}, ErrAgentUnauthorized
		}
		if err != nil {
			return EnrollmentResult{}, fmt.Errorf("read relay parent: %w", err)
		}
		block, _ := pem.Decode(priorPEM)
		if block == nil {
			return EnrollmentResult{}, errors.New("invalid stored relay certificate")
		}
		prior, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return EnrollmentResult{}, fmt.Errorf("parse stored relay certificate: %w", err)
		}
		priorKey, ok := prior.PublicKey.(ed25519.PublicKey)
		if !ok || !bytes.Equal(key, priorKey) {
			return EnrollmentResult{}, ErrInvalidCSR
		}
		if expiry.Sub(now) > CertificateRenewalWindow {
			return EnrollmentResult{}, ErrRenewalTooEarly
		}
	}
	var active int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM agent_relay_certificate_grants WHERE node_id=$1`, nodeID).Scan(&active); err != nil {
		return EnrollmentResult{}, fmt.Errorf("count relay grants: %w", err)
	}
	if active >= 2 {
		return EnrollmentResult{}, ErrRenewalGrantLimit
	}
	issued, err := service.issuer.IssueRelayServerCertificate(csrPEM, nodeID, host, now)
	if err != nil {
		return EnrollmentResult{}, fmt.Errorf("sign relay certificate: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO agent_relay_certificate_grants(node_id,fingerprint,parent_fingerprint,csr_digest,certificate_pem,host,expires_at)
 VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7)`, nodeID, issued.Fingerprint, parent, csrDigest, issued.CertificatePEM, host, issued.ExpiresAt); err != nil {
		return EnrollmentResult{}, fmt.Errorf("store relay certificate: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return EnrollmentResult{}, fmt.Errorf("commit relay certificate: %w", err)
	}
	return EnrollmentResult{NodeID: nodeID, CertificatePEM: issued.CertificatePEM, CACertificatePEM: service.relayCAPEM(), Fingerprint: issued.Fingerprint, ExpiresAt: issued.ExpiresAt}, nil
}

func (service *EnrollmentService) relayCAPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: service.issuer.ca.Raw})
}
