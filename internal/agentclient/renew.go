package agentclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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

func RenewCertificate(ctx context.Context, endpoint string, roots *x509.CertPool, pair tls.Certificate,
	keyPEM []byte, version, expectedNodeID string) (Credentials, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "/api/v1/agent/renew" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || roots == nil ||
		version == "" || expectedNodeID == "" || len(pair.Certificate) == 0 {
		return Credentials{}, errors.New("invalid Agent renewal configuration")
	}
	private, ok := pair.PrivateKey.(ed25519.PrivateKey)
	if !ok {
		return Credentials{}, errors.New("Agent renewal requires Ed25519 private key")
	}
	if _, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]}), keyPEM); err != nil {
		return Credentials{}, fmt.Errorf("Agent renewal key mismatch: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{}}, private)
	if err != nil {
		return Credentials{}, fmt.Errorf("create Agent renewal CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	body, err := json.Marshal(struct {
		CSRPEM  string `json:"csr_pem"`
		Version string `json:"version"`
	}{string(csrPEM), version})
	if err != nil {
		return Credentials{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Credentials{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots,
		Certificates: []tls.Certificate{pair}}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport}
	response, err := client.Do(request)
	if err != nil {
		return Credentials{}, fmt.Errorf("request Agent renewal: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Credentials{}, fmt.Errorf("Agent renewal returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 128<<10))
	decoder.DisallowUnknownFields()
	var result struct {
		NodeID           string    `json:"node_id"`
		CertificatePEM   string    `json:"certificate_pem"`
		CACertificatePEM string    `json:"ca_certificate_pem"`
		ExpiresAt        time.Time `json:"expires_at"`
	}
	if err := decoder.Decode(&result); err != nil {
		return Credentials{}, fmt.Errorf("decode Agent renewal: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Credentials{}, errors.New("Agent renewal response has trailing JSON")
	}
	certBlock, certRest := pem.Decode([]byte(result.CertificatePEM))
	caBlock, caRest := pem.Decode([]byte(result.CACertificatePEM))
	if certBlock == nil || certBlock.Type != "CERTIFICATE" || len(bytes.TrimSpace(certRest)) != 0 ||
		caBlock == nil || caBlock.Type != "CERTIFICATE" || len(bytes.TrimSpace(caRest)) != 0 {
		return Credentials{}, errors.New("Agent renewal response lacks certificates")
	}
	certificate, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return Credentials{}, err
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return Credentials{}, err
	}
	identity, err := agentidentity.CertificateNodeID(certificate)
	if err != nil || identity != expectedNodeID || result.NodeID != expectedNodeID || !result.ExpiresAt.Equal(certificate.NotAfter) {
		return Credentials{}, errors.New("Agent renewal certificate identity mismatch")
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(ca)
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: clientRoots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return Credentials{}, fmt.Errorf("verify Agent renewed certificate: %w", err)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return Credentials{}, fmt.Errorf("verify Agent renewed certificate against pinned CA: %w", err)
	}
	if _, err := tls.X509KeyPair([]byte(result.CertificatePEM), keyPEM); err != nil {
		return Credentials{}, errors.New("Agent renewed certificate does not match local key")
	}
	return Credentials{NodeID: result.NodeID, CertPEM: []byte(result.CertificatePEM), KeyPEM: keyPEM,
		CAPEM: []byte(result.CACertificatePEM), ExpiresAt: result.ExpiresAt}, nil
}

// SaveRenewedCertificate replaces only the certificate; the existing key stays
// unchanged. Both the temporary file and directory are synced before success.
func SaveRenewedCertificate(certPath, keyPath string, certPEM []byte) error {
	if !filepath.IsAbs(certPath) || !filepath.IsAbs(keyPath) || certPath == keyPath {
		return errors.New("invalid Agent credential paths")
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read Agent renewal key: %w", err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("invalid renewed Agent certificate: %w", err)
	}
	info, err := os.Lstat(certPath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("Agent certificate path is not a regular file")
	}
	directory := filepath.Dir(certPath)
	file, err := os.CreateTemp(directory, ".agent-renewal-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(certPEM); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), certPath); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
