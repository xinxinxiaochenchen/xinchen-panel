package agentrelay

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"regexp"

	"controlplane/internal/agentidentity"
)

var certificateFingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ServerTLSConfig verifies the Agent CA chain and asks the caller to check
// the live node/fingerprint authorization. Route-specific previous-node
// authorization still occurs after OPEN supplies the line identity.
func ServerTLSConfig(getCertificate func() (*tls.Certificate, error), clientRoots *x509.CertPool,
	authorized func(nodeID, fingerprint string) bool) (*tls.Config, error) {
	if getCertificate == nil || clientRoots == nil || authorized == nil {
		return nil, errors.New("relay server TLS credentials and authorization are required")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return getCertificate() },
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) != 1 {
				return errors.New("unverified relay source certificate")
			}
			peer := state.PeerCertificates[0]
			nodeID, err := agentidentity.CertificateNodeID(peer)
			if err != nil {
				return errors.New("invalid relay source identity")
			}
			if !authorized(nodeID, certificateFingerprint(peer)) {
				return errors.New("relay source is not authorized")
			}
			return nil
		}}, nil
}

// ClientTLSConfig uses normal TLS chain, server EKU and DNS/IP SAN checks,
// then pins the expected relay node URI and one of its currently authorized
// fingerprints. The fingerprints cover certificate renewal overlap.
func ClientTLSConfig(getCertificate func() (*tls.Certificate, error), roots *x509.CertPool,
	host, expectedNodeID string, fingerprints []string) (*tls.Config, error) {
	if getCertificate == nil || roots == nil || host == "" || !uuidPattern.MatchString(expectedNodeID) ||
		len(fingerprints) < 1 || len(fingerprints) > 2 {
		return nil, errors.New("invalid relay destination TLS configuration")
	}
	for _, fingerprint := range fingerprints {
		if !certificateFingerprintPattern.MatchString(fingerprint) {
			return nil, errors.New("invalid relay destination certificate fingerprint")
		}
	}
	fingerprints = append([]string(nil), fingerprints...)
	return &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13,
		ServerName: host, RootCAs: roots,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return getCertificate() },
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) != 1 {
				return errors.New("unverified relay destination certificate")
			}
			peer := state.PeerCertificates[0]
			nodeID, err := agentidentity.CertificateRelayNodeID(peer)
			if err != nil || nodeID != expectedNodeID {
				return errors.New("relay destination identity mismatch")
			}
			fingerprint := certificateFingerprint(peer)
			for _, allowed := range fingerprints {
				if fingerprint == allowed {
					return nil
				}
			}
			return errors.New("relay destination certificate is not authorized")
		}}, nil
}

func certificateFingerprint(certificate *x509.Certificate) string {
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}
