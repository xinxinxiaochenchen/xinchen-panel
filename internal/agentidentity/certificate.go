package agentidentity

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"time"
)

const ClientCertificateLifetime = 24 * time.Hour

var nodeIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type Issuer struct {
	ca     *x509.Certificate
	signer crypto.Signer
}

type IssuedCertificate struct {
	CertificatePEM []byte
	Fingerprint    string
	ExpiresAt      time.Time
}

func NewIssuer(certificatePEM, privateKeyPEM []byte) (*Issuer, error) {
	certificateBlock, certificateRest := pem.Decode(certificatePEM)
	if certificateBlock == nil || certificateBlock.Type != "CERTIFICATE" || len(bytes.TrimSpace(certificateRest)) != 0 {
		return nil, errors.New("invalid Agent CA certificate PEM")
	}
	ca, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse Agent CA certificate: %w", err)
	}
	if !ca.IsCA || ca.KeyUsage&x509.KeyUsageCertSign == 0 || !ca.BasicConstraintsValid ||
		time.Now().Before(ca.NotBefore) || !time.Now().Before(ca.NotAfter) {
		return nil, errors.New("Agent CA cannot sign valid certificates")
	}
	keyBlock, keyRest := pem.Decode(privateKeyPEM)
	if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" || len(bytes.TrimSpace(keyRest)) != 0 {
		return nil, errors.New("invalid Agent CA private key PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse Agent CA private key: %w", err)
	}
	signer, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, errors.New("Agent CA private key cannot sign")
	}
	keyPublic, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return nil, fmt.Errorf("encode Agent CA public key: %w", err)
	}
	certificatePublic, err := x509.MarshalPKIXPublicKey(ca.PublicKey)
	if err != nil || !bytes.Equal(keyPublic, certificatePublic) {
		return nil, errors.New("Agent CA certificate and private key do not match")
	}
	return &Issuer{ca: ca, signer: signer}, nil
}

func (issuer *Issuer) IssueClientCertificate(csrPEM []byte, nodeID string, now time.Time) (IssuedCertificate, error) {
	return issuer.issueCertificate(csrPEM, nodeID, "", now)
}

func (issuer *Issuer) issueCertificate(csrPEM []byte, nodeID, relayHost string, now time.Time) (IssuedCertificate, error) {
	if issuer == nil || issuer.ca == nil || issuer.signer == nil {
		return IssuedCertificate{}, errors.New("Agent CA issuer is unavailable")
	}
	if !nodeIDPattern.MatchString(nodeID) {
		return IssuedCertificate{}, errors.New("invalid Agent node ID")
	}
	block, rest := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return IssuedCertificate{}, errors.New("invalid Agent certificate request PEM")
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("parse Agent certificate request: %w", err)
	}
	if err := request.CheckSignature(); err != nil {
		return IssuedCertificate{}, fmt.Errorf("verify Agent certificate request: %w", err)
	}
	if _, ok := request.PublicKey.(ed25519.PublicKey); !ok {
		return IssuedCertificate{}, errors.New("Agent certificate request must use Ed25519")
	}
	now = now.UTC().Truncate(time.Second)
	if now.Before(issuer.ca.NotBefore) || now.Add(ClientCertificateLifetime).After(issuer.ca.NotAfter) {
		return IssuedCertificate{}, errors.New("Agent CA validity is too short")
	}
	serialBytes := make([]byte, 16)
	if _, err := rand.Read(serialBytes); err != nil {
		return IssuedCertificate{}, fmt.Errorf("generate Agent certificate serial: %w", err)
	}
	serialBytes[0] &= 0x7f
	serial := new(big.Int).SetBytes(serialBytes)
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	identity, err := url.Parse("spiffe://network-control-plane/agent/" + nodeID)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("build Agent identity URI: %w", err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: nodeID},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(ClientCertificateLifetime),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs: []*url.URL{identity}, BasicConstraintsValid: true}
	if relayHost != "" {
		identity.Path = "/relay/" + nodeID
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		if address, err := netip.ParseAddr(relayHost); err == nil {
			template.IPAddresses = []net.IP{net.IP(address.AsSlice())}
		} else {
			template.DNSNames = []string{relayHost}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer.ca, request.PublicKey, issuer.signer)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("sign Agent certificate: %w", err)
	}
	fingerprint := sha256.Sum256(der)
	return IssuedCertificate{CertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		Fingerprint: hex.EncodeToString(fingerprint[:]), ExpiresAt: template.NotAfter}, nil
}
