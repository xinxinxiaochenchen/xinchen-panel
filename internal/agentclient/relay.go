package agentclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"controlplane/internal/agentidentity"
)

// RequestRelayCertificate uses the separately persisted relay Ed25519 key and
// exchanges its CSR over the authenticated Agent channel. The private key
// never enters the request body; the response is checked against the pinned
// CA, node URI, server SAN and the persisted key.
func RequestRelayCertificate(ctx context.Context, endpoint string, roots *x509.CertPool, agentPair tls.Certificate, agentKeyPEM, relayKeyPEM []byte, parent, expectedNodeID, expectedHost string) (Credentials, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "/api/v1/agent/relay-certificate" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || roots == nil || expectedNodeID == "" || expectedHost == "" || len(agentPair.Certificate) == 0 {
		return Credentials{}, errors.New("invalid Agent relay certificate configuration")
	}
	if _, ok := agentPair.PrivateKey.(ed25519.PrivateKey); !ok {
		return Credentials{}, errors.New("Agent relay request requires Ed25519 client key")
	}
	if _, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: agentPair.Certificate[0]}), agentKeyPEM); err != nil {
		return Credentials{}, fmt.Errorf("Agent relay client key mismatch: %w", err)
	}
	relayBlock, relayRest := pem.Decode(relayKeyPEM)
	if relayBlock == nil || relayBlock.Type != "PRIVATE KEY" || len(bytes.TrimSpace(relayRest)) != 0 {
		return Credentials{}, errors.New("invalid local relay private key")
	}
	parsedRelayKey, err := x509.ParsePKCS8PrivateKey(relayBlock.Bytes)
	if err != nil {
		return Credentials{}, errors.New("invalid local relay private key")
	}
	relayKey, ok := parsedRelayKey.(ed25519.PrivateKey)
	if !ok {
		return Credentials{}, errors.New("relay private key must be Ed25519")
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{}}, relayKey)
	if err != nil {
		return Credentials{}, fmt.Errorf("create relay CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	body, _ := json.Marshal(struct {
		CSRPEM string `json:"csr_pem"`
		Parent string `json:"parent_fingerprint,omitempty"`
	}{string(csrPEM), parent})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{agentPair}, ServerName: parsed.Hostname()}}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Timeout: 15 * time.Second, Transport: transport}).Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("request relay certificate: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Credentials{}, fmt.Errorf("relay certificate returned HTTP %d", response.StatusCode)
	}
	var result struct {
		NodeID           string    `json:"node_id"`
		CertificatePEM   string    `json:"certificate_pem"`
		CACertificatePEM string    `json:"ca_certificate_pem"`
		Fingerprint      string    `json:"fingerprint"`
		ExpiresAt        time.Time `json:"expires_at"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Credentials{}, fmt.Errorf("decode relay certificate: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Credentials{}, errors.New("relay certificate response has trailing JSON")
	}
	cb, rest := pem.Decode([]byte(result.CertificatePEM))
	cab, carest := pem.Decode([]byte(result.CACertificatePEM))
	if cb == nil || cb.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 || cab == nil || cab.Type != "CERTIFICATE" || len(bytes.TrimSpace(carest)) != 0 {
		return Credentials{}, errors.New("relay response lacks certificates")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return Credentials{}, err
	}
	ca, err := x509.ParseCertificate(cab.Bytes)
	if err != nil {
		return Credentials{}, err
	}
	if result.NodeID != expectedNodeID || result.ExpiresAt.IsZero() || !result.ExpiresAt.Equal(cert.NotAfter) {
		return Credentials{}, errors.New("relay certificate identity mismatch")
	}
	identity, err := agentidentity.CertificateRelayNodeID(cert)
	if err != nil || identity != expectedNodeID {
		return Credentials{}, errors.New("relay certificate node identity mismatch")
	}
	if err := cert.VerifyHostname(expectedHost); err != nil {
		return Credentials{}, fmt.Errorf("relay certificate host mismatch: %w", err)
	}
	caRoots := x509.NewCertPool()
	caRoots.AddCert(ca)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: caRoots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return Credentials{}, fmt.Errorf("verify relay certificate: %w", err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return Credentials{}, fmt.Errorf("verify relay certificate against pinned CA: %w", err)
	}
	fingerprint := sha256.Sum256(cert.Raw)
	if result.Fingerprint != hex.EncodeToString(fingerprint[:]) {
		return Credentials{}, errors.New("relay certificate fingerprint mismatch")
	}
	if _, err := tls.X509KeyPair([]byte(result.CertificatePEM), relayKeyPEM); err != nil {
		return Credentials{}, errors.New("relay certificate does not match generated key")
	}
	return Credentials{NodeID: result.NodeID, CertPEM: []byte(result.CertificatePEM), KeyPEM: relayKeyPEM, CAPEM: []byte(result.CACertificatePEM), ExpiresAt: result.ExpiresAt}, nil
}

// GenerateRelayKey creates a relay-only Ed25519 key for local persistence
// before the first request. Reuse it for retries and same-key renewals.
func GenerateRelayKey() ([]byte, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// EnsureRelayKey loads an existing owner-only relay key or creates one with
// an atomic 0600 write. It is safe to call at every Agent start.
func EnsureRelayKey(path string) ([]byte, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, errors.New("invalid relay key path")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("relay key must be owner-only regular file")
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !validEd25519Key(value) {
			return nil, errors.New("invalid relay key")
		}
		return value, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	value, err := GenerateRelayKey()
	if err != nil {
		return nil, err
	}
	if err := atomicWrite0600(path, value, false); err != nil {
		return nil, err
	}
	return value, nil
}
func SaveRelayCertificate(certPath, keyPath string, certPEM []byte) error {
	if !filepath.IsAbs(certPath) || !filepath.IsAbs(keyPath) || certPath == keyPath {
		return errors.New("invalid relay credential paths")
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	if !validEd25519Key(keyPEM) {
		return errors.New("invalid relay key")
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("relay certificate key mismatch: %w", err)
	}
	if info, err := os.Lstat(certPath); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("relay certificate path is not regular")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return atomicWrite0600(certPath, certPEM, true)
}
func validEd25519Key(value []byte) bool {
	block, rest := pem.Decode(value)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return false
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	_, ok := key.(ed25519.PrivateKey)
	return err == nil && ok
}
func atomicWrite0600(path string, value []byte, replace bool) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".relay-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(value); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if !replace {
		if err = os.Link(tmp, path); err != nil {
			return err
		}
	} else if err = os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
