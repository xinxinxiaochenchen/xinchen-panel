package agentidentity

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrAgentUnauthorized = errors.New("Agent certificate is not authorized")

// CertificateNodeID extracts only the control plane's dedicated Agent URI.
// The TLS server must also verify the certificate chain before using it.
func CertificateNodeID(certificate *x509.Certificate) (string, error) {
	if certificate == nil || len(certificate.URIs) != 1 ||
		len(certificate.ExtKeyUsage) != 1 || certificate.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth ||
		certificate.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return "", ErrAgentUnauthorized
	}
	if _, ok := certificate.PublicKey.(ed25519.PublicKey); !ok {
		return "", ErrAgentUnauthorized
	}
	identity := certificate.URIs[0]
	if identity.Scheme != "spiffe" || identity.Host != "network-control-plane" ||
		identity.User != nil || identity.RawQuery != "" || identity.Fragment != "" || identity.RawFragment != "" {
		return "", ErrAgentUnauthorized
	}
	const prefix = "/agent/"
	if !strings.HasPrefix(identity.Path, prefix) {
		return "", ErrAgentUnauthorized
	}
	nodeID := strings.TrimPrefix(identity.Path, prefix)
	if !nodeIDPattern.MatchString(nodeID) || certificate.Subject.CommonName != nodeID {
		return "", ErrAgentUnauthorized
	}
	return strings.ToLower(nodeID), nil
}

// AuthenticateCertificate performs the online revocation check needed both at
// connection establishment and periodically for a long-lived Agent stream.
func (service *EnrollmentService) AuthenticateCertificate(ctx context.Context, certificate *x509.Certificate) (string, error) {
	nodeID, err := CertificateNodeID(certificate)
	if err != nil {
		return "", err
	}
	roots := x509.NewCertPool()
	roots.AddCert(service.issuer.ca)
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: time.Now()}); err != nil {
		return "", ErrAgentUnauthorized
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	var allowed bool
	if err := service.pool.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM agents a JOIN nodes n ON n.id=a.node_id
WHERE a.node_id=$1 AND a.status <> 'revoked' AND n.enabled
AND ((a.cert_fingerprint=$2 AND a.cert_expires_at > clock_timestamp()) OR EXISTS
(SELECT 1 FROM agent_certificate_grants g WHERE g.node_id=a.node_id AND g.fingerprint=$2
AND g.expires_at > clock_timestamp())))`, nodeID, hex.EncodeToString(fingerprint[:])).Scan(&allowed); err != nil {
		return "", fmt.Errorf("check Agent certificate authorization: %w", err)
	}
	if !allowed {
		return "", ErrAgentUnauthorized
	}
	return nodeID, nil
}
