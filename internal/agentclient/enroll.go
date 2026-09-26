package agentclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
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

type Credentials struct {
	NodeID    string
	CertPEM   []byte
	KeyPEM    []byte
	CAPEM     []byte
	ExpiresAt time.Time
}

func Enroll(ctx context.Context, endpoint string, roots *x509.CertPool, token, version, expectedNodeID string) (Credentials, error) {
	parsed, err := url.Parse(endpoint)
	decodedToken, tokenErr := base64.RawURLEncoding.DecodeString(token)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "/api/v1/agent/enroll" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || roots == nil ||
		tokenErr != nil || len(decodedToken) != 32 || expectedNodeID == "" || version == "" {
		return Credentials{}, errors.New("invalid Agent enrollment configuration")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Credentials{}, fmt.Errorf("generate Agent key: %w", err)
	}
	_ = public
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{}}, private)
	if err != nil {
		return Credentials{}, fmt.Errorf("create Agent CSR: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return Credentials{}, fmt.Errorf("marshal Agent key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	body, err := json.Marshal(struct {
		Token   string `json:"token"`
		CSRPEM  string `json:"csr_pem"`
		Version string `json:"version"`
	}{Token: token, CSRPEM: string(csrPEM), Version: version})
	if err != nil {
		return Credentials{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Credentials{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots}}}
	response, err := client.Do(request)
	if err != nil {
		return Credentials{}, fmt.Errorf("request Agent enrollment: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return Credentials{}, fmt.Errorf("Agent enrollment returned HTTP %d", response.StatusCode)
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
		return Credentials{}, fmt.Errorf("decode Agent enrollment response: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Credentials{}, errors.New("Agent enrollment response has trailing JSON")
	}
	certBlock, _ := pem.Decode([]byte(result.CertificatePEM))
	caBlock, _ := pem.Decode([]byte(result.CACertificatePEM))
	if certBlock == nil || caBlock == nil || certBlock.Type != "CERTIFICATE" || caBlock.Type != "CERTIFICATE" {
		return Credentials{}, errors.New("Agent enrollment response lacks certificates")
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
		return Credentials{}, errors.New("Agent enrollment certificate identity mismatch")
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(ca)
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: clientRoots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return Credentials{}, fmt.Errorf("verify Agent enrollment certificate: %w", err)
	}
	if _, err := tls.X509KeyPair([]byte(result.CertificatePEM), keyPEM); err != nil {
		return Credentials{}, errors.New("Agent enrollment certificate does not match local key")
	}
	return Credentials{NodeID: result.NodeID, CertPEM: []byte(result.CertificatePEM), KeyPEM: keyPEM,
		CAPEM: []byte(result.CACertificatePEM), ExpiresAt: result.ExpiresAt}, nil
}

func SaveCredentials(certPath, keyPath string, value Credentials) error {
	if !filepath.IsAbs(certPath) || !filepath.IsAbs(keyPath) || certPath == keyPath ||
		len(value.CertPEM) == 0 || len(value.KeyPEM) == 0 {
		return errors.New("invalid Agent credential paths")
	}
	if _, err := tls.X509KeyPair(value.CertPEM, value.KeyPEM); err != nil {
		return err
	}
	write := func(path string, content []byte) error {
		file, err := os.CreateTemp(filepath.Dir(path), ".agent-credential-*")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		if err := file.Chmod(0600); err != nil {
			file.Close()
			return err
		}
		if _, err := file.Write(content); err != nil {
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
		return os.Link(file.Name(), path)
	}
	if err := write(keyPath, value.KeyPEM); err != nil {
		return fmt.Errorf("save Agent private key: %w", err)
	}
	if err := write(certPath, value.CertPEM); err != nil {
		return fmt.Errorf("save Agent certificate (new key retained at %s): %w", keyPath, err)
	}
	return nil
}
