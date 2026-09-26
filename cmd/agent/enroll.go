package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"controlplane/internal/agentclient"
)

type EnrollmentConfig struct {
	URL      string
	NodeID   string
	Version  string
	CAFile   string
	CertFile string
	KeyFile  string
}

func LoadEnrollmentConfig(lookup func(string) (string, bool)) (EnrollmentConfig, error) {
	var cfg EnrollmentConfig
	fields := []struct {
		key    string
		target *string
	}{
		{"CONTROL_AGENT_ENROLL_URL", &cfg.URL}, {"CONTROL_AGENT_NODE_ID", &cfg.NodeID},
		{"CONTROL_AGENT_VERSION", &cfg.Version}, {"CONTROL_AGENT_CA_CERT_FILE", &cfg.CAFile},
		{"CONTROL_AGENT_CERT_FILE", &cfg.CertFile}, {"CONTROL_AGENT_KEY_FILE", &cfg.KeyFile},
	}
	for _, field := range fields {
		value, ok := lookup(field.key)
		if !ok || value == "" {
			return EnrollmentConfig{}, fmt.Errorf("%s is required", field.key)
		}
		*field.target = value
	}
	endpoint, err := url.Parse(cfg.URL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil ||
		endpoint.Path != "/api/v1/agent/enroll" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return EnrollmentConfig{}, errors.New("Agent enrollment requires the dedicated HTTPS endpoint")
	}
	if !agentUUID.MatchString(cfg.NodeID) || !agentVersion.MatchString(cfg.Version) {
		return EnrollmentConfig{}, errors.New("Agent node or version is invalid")
	}
	for _, path := range []string{cfg.CAFile, cfg.CertFile, cfg.KeyFile} {
		if !filepath.IsAbs(path) {
			return EnrollmentConfig{}, errors.New("Agent enrollment paths must be absolute")
		}
	}
	info, err := os.Stat(cfg.CAFile)
	if err != nil || !info.Mode().IsRegular() {
		return EnrollmentConfig{}, errors.New("Agent CA certificate unavailable")
	}
	for _, path := range []string{cfg.CertFile, cfg.KeyFile} {
		if _, err := os.Lstat(path); err == nil {
			return EnrollmentConfig{}, errors.New("Agent credentials already exist")
		} else if !os.IsNotExist(err) {
			return EnrollmentConfig{}, err
		}
	}
	return cfg, nil
}

func enrollAgent(ctx context.Context, cfg EnrollmentConfig, token string) error {
	caPEM, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return errors.New("Agent CA certificate is invalid")
	}
	credentials, err := agentclient.Enroll(ctx, cfg.URL, roots, strings.TrimSpace(token), cfg.Version, cfg.NodeID)
	if err != nil {
		return err
	}
	return agentclient.SaveCredentials(cfg.CertFile, cfg.KeyFile, credentials)
}
