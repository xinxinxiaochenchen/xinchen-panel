package agentidentity

import (
	"crypto/x509"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

var relayDNSLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// IssueRelayServerCertificate must only receive the node's persisted relay
// address after the calling service authorizes the authenticated Agent. CSR
// identity and SANs are ignored; the Agent private key never leaves the Agent.
func (issuer *Issuer) IssueRelayServerCertificate(csrPEM []byte, nodeID, host string, now time.Time) (IssuedCertificate, error) {
	if !validRelayHost(host) {
		return IssuedCertificate{}, errors.New("invalid relay server address")
	}
	return issuer.issueCertificate(csrPEM, nodeID, host, now)
}

func validRelayHost(host string) bool {
	if address, err := netip.ParseAddr(host); err == nil {
		return address.Zone() == "" && !address.IsUnspecified() && !address.IsMulticast()
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) > 63 || !relayDNSLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// CertificateRelayNodeID extracts the relay-only identity after TLS has
// verified the CA, server EKU, expiry and configured DNS/IP SAN.
func CertificateRelayNodeID(certificate *x509.Certificate) (string, error) {
	return certificateNodeID(certificate, x509.ExtKeyUsageServerAuth, "/relay/")
}
