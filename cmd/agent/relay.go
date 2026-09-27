package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"

	"controlplane/internal/agentclient"
	"controlplane/internal/agentidentity"
	"controlplane/internal/agentrelay"
	"controlplane/internal/agentruntime"
)

type relayRuntimeState struct {
	cfg   AgentConfig
	roots *x509.CertPool
}

func prepareRelay(ctx context.Context, cfg AgentConfig, agentPair tls.Certificate, agentKeyPEM []byte, roots *x509.CertPool) (*relayRuntimeState, error) {
	if cfg.RelayHost == "" {
		return nil, nil
	}
	keyPEM, err := agentclient.EnsureRelayKey(cfg.RelayKeyFile)
	if err != nil {
		return nil, fmt.Errorf("prepare relay key: %w", err)
	}
	var existing tls.Certificate
	if pair, loadErr := tls.LoadX509KeyPair(cfg.RelayCertFile, cfg.RelayKeyFile); loadErr == nil {
		if err := validateRelayCertificate(pair, roots, cfg.NodeID, cfg.RelayHost); err == nil {
			existing = pair
		}
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		// A malformed or partial credential is replaced only after a successful
		// control-plane issuance; keep the original error as context below.
		existing = tls.Certificate{}
	}
	if len(existing.Certificate) == 0 || time.Until(relayLeaf(existing).NotAfter) <= agentidentity.CertificateRenewalWindow {
		parent := ""
		if len(existing.Certificate) > 0 {
			parent = relayFingerprint(existing)
		}
		credentials, requestErr := requestRelay(ctx, cfg, roots, agentPair, agentKeyPEM, keyPEM, parent)
		if requestErr != nil {
			if len(existing.Certificate) == 0 || relayLeaf(existing).NotAfter.Before(time.Now()) {
				return nil, requestErr
			}
		} else if err := agentclient.SaveRelayCertificate(cfg.RelayCertFile, cfg.RelayKeyFile, credentials.CertPEM); err != nil {
			return nil, fmt.Errorf("save relay certificate: %w", err)
		} else {
			existing, err = tls.LoadX509KeyPair(cfg.RelayCertFile, cfg.RelayKeyFile)
			if err != nil {
				return nil, fmt.Errorf("reload relay certificate: %w", err)
			}
		}
	}
	if err := validateRelayCertificate(existing, roots, cfg.NodeID, cfg.RelayHost); err != nil {
		return nil, fmt.Errorf("validate relay certificate: %w", err)
	}
	return &relayRuntimeState{cfg: cfg, roots: roots}, nil
}

func (s *relayRuntimeState) serverTLS() (*tls.Config, error) {
	return agentrelay.ServerTLSConfig(func() (*tls.Certificate, error) {
		pair, err := tls.LoadX509KeyPair(s.cfg.RelayCertFile, s.cfg.RelayKeyFile)
		if err != nil {
			return nil, err
		}
		return &pair, nil
	}, s.roots, func(string, string) bool {
		// The route snapshot binds the verified Agent identity to the expected
		// previous node after the TLS handshake. The CA check here rejects
		// certificates outside this control plane before the route is consulted.
		return true
	})
}

func (s *relayRuntimeState) clientTLS(_ context.Context, next agentruntime.RelayNextHop) (*tls.Config, error) {
	host, _, err := net.SplitHostPort(next.Address)
	if err != nil || host == "" {
		return nil, errors.New("invalid relay next-hop address")
	}
	return agentrelay.ClientTLSConfig(func() (*tls.Certificate, error) {
		pair, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
		if err != nil {
			return nil, err
		}
		return &pair, nil
	}, s.roots, host, next.NodeID, next.Fingerprints)
}

func (s *relayRuntimeState) renewLoop(ctx context.Context, agentKeyFile string) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		if err := s.renewIfNeeded(ctx, agentKeyFile); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "relay certificate renewal failed: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *relayRuntimeState) renewIfNeeded(ctx context.Context, agentKeyFile string) error {
	pair, err := tls.LoadX509KeyPair(s.cfg.RelayCertFile, s.cfg.RelayKeyFile)
	if err != nil {
		return err
	}
	if pair.Leaf == nil {
		pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return err
		}
	}
	remaining := time.Until(pair.Leaf.NotAfter)
	if remaining > agentidentity.CertificateRenewalWindow {
		return nil
	}
	agentPair, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
	if err != nil {
		return err
	}
	agentKeyPEM, err := os.ReadFile(agentKeyFile)
	if err != nil {
		return err
	}
	relayKeyPEM, err := os.ReadFile(s.cfg.RelayKeyFile)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	credentials, err := requestRelay(requestCtx, s.cfg, s.roots, agentPair, agentKeyPEM, relayKeyPEM, relayFingerprint(pair))
	if err != nil {
		return err
	}
	return agentclient.SaveRelayCertificate(s.cfg.RelayCertFile, s.cfg.RelayKeyFile, credentials.CertPEM)
}

func requestRelay(ctx context.Context, cfg AgentConfig, roots *x509.CertPool, agentPair tls.Certificate, agentKeyPEM, relayKeyPEM []byte, parent string) (agentclient.Credentials, error) {
	endpoint, err := url.Parse(cfg.StreamURL)
	if err != nil {
		return agentclient.Credentials{}, err
	}
	endpoint.Scheme = "https"
	endpoint.Path = "/api/v1/agent/relay-certificate"
	endpoint.RawQuery, endpoint.Fragment = "", ""
	return agentclient.RequestRelayCertificate(ctx, endpoint.String(), roots, agentPair, agentKeyPEM, relayKeyPEM, parent, cfg.NodeID, cfg.RelayHost)
}

func validateRelayCertificate(pair tls.Certificate, roots *x509.CertPool, nodeID, host string) error {
	if len(pair.Certificate) == 0 || roots == nil {
		return errors.New("relay certificate is empty")
	}
	certificate := pair.Leaf
	if certificate == nil {
		var err error
		certificate, err = x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return err
		}
	}
	identity, err := agentidentity.CertificateRelayNodeID(certificate)
	if err != nil || identity != nodeID {
		return errors.New("relay certificate identity mismatch")
	}
	if err := certificate.VerifyHostname(host); err != nil {
		return err
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return err
	}
	if !time.Now().Before(certificate.NotAfter) {
		return errors.New("relay certificate is expired")
	}
	return nil
}

func relayFingerprint(pair tls.Certificate) string {
	sum := sha256.Sum256(pair.Certificate[0])
	return hex.EncodeToString(sum[:])
}

// relayLeaf avoids making credential selection depend on tls.LoadX509KeyPair
// populating Leaf (which differs between Go versions).
func relayLeaf(pair tls.Certificate) *x509.Certificate {
	if pair.Leaf != nil {
		return pair.Leaf
	}
	if len(pair.Certificate) == 0 {
		return &x509.Certificate{}
	}
	certificate, _ := x509.ParseCertificate(pair.Certificate[0])
	if certificate == nil {
		return &x509.Certificate{}
	}
	return certificate
}
