package httpapi

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"controlplane/internal/platform/config"
)

// LoadAgentTLS validates that the dedicated Agent listener has a usable
// server identity and trusts only the configured Agent CA for client certs.
func LoadAgentTLS(cfg config.Config) (*tls.Config, error) {
	if cfg.AgentTLSAddr == "" {
		return nil, errors.New("Agent TLS listener is not configured")
	}
	serverIdentity, err := tls.LoadX509KeyPair(cfg.AgentTLSCertFile, cfg.AgentTLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load Agent TLS server identity: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.AgentCACertFile)
	if err != nil {
		return nil, fmt.Errorf("read Agent client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("Agent client CA file contains no valid certificate")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverIdentity},
		ClientCAs: pool, ClientAuth: tls.VerifyClientCertIfGiven}, nil
}
