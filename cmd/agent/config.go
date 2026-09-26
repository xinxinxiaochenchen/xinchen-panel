package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
)

var agentUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var agentVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

type AgentConfig struct {
	StreamURL     string
	NodeID        string
	Version       string
	CACertFile    string
	CertFile      string
	KeyFile       string
	StateFile     string
	BindHost      string
	ProxyCertFile string
	ProxyKeyFile  string
}

func LoadAgentConfig(lookup func(string) (string, bool)) (AgentConfig, error) {
	var cfg AgentConfig
	fields := []struct {
		key    string
		target *string
	}{
		{"CONTROL_AGENT_STREAM_URL", &cfg.StreamURL}, {"CONTROL_AGENT_NODE_ID", &cfg.NodeID},
		{"CONTROL_AGENT_VERSION", &cfg.Version}, {"CONTROL_AGENT_CA_CERT_FILE", &cfg.CACertFile},
		{"CONTROL_AGENT_CERT_FILE", &cfg.CertFile}, {"CONTROL_AGENT_KEY_FILE", &cfg.KeyFile},
		{"CONTROL_AGENT_STATE_FILE", &cfg.StateFile},
	}
	for _, field := range fields {
		value, ok := lookup(field.key)
		if !ok || value == "" {
			return AgentConfig{}, fmt.Errorf("%s is required", field.key)
		}
		*field.target = value
	}
	endpoint, err := url.Parse(cfg.StreamURL)
	if err != nil || endpoint.Scheme != "wss" || endpoint.Host == "" || endpoint.User != nil ||
		endpoint.Path != "/api/v1/agent/stream" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return AgentConfig{}, errors.New("CONTROL_AGENT_STREAM_URL must be a WSS Agent stream URL")
	}
	if !agentUUID.MatchString(cfg.NodeID) || !agentVersion.MatchString(cfg.Version) {
		return AgentConfig{}, errors.New("Agent node ID or version is invalid")
	}
	for _, path := range []string{cfg.CACertFile, cfg.CertFile, cfg.KeyFile} {
		if !filepath.IsAbs(path) {
			return AgentConfig{}, errors.New("Agent certificate paths must be absolute")
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return AgentConfig{}, fmt.Errorf("Agent certificate file unavailable: %s", path)
		}
	}
	info, err := os.Stat(cfg.KeyFile)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return AgentConfig{}, errors.New("Agent private key must be readable only by owner")
	}
	if !filepath.IsAbs(cfg.StateFile) {
		return AgentConfig{}, errors.New("Agent state path must be absolute")
	}
	cfg.BindHost = "0.0.0.0"
	if value, ok := lookup("CONTROL_AGENT_BIND_HOST"); ok && value != "" {
		if net.ParseIP(value) == nil {
			return AgentConfig{}, errors.New("CONTROL_AGENT_BIND_HOST must be an IP address")
		}
		cfg.BindHost = value
	}
	cfg.ProxyCertFile, _ = lookup("CONTROL_AGENT_PROXY_CERT_FILE")
	cfg.ProxyKeyFile, _ = lookup("CONTROL_AGENT_PROXY_KEY_FILE")
	if cfg.ProxyCertFile != "" || cfg.ProxyKeyFile != "" {
		for _, path := range []string{cfg.ProxyCertFile, cfg.ProxyKeyFile} {
			info, err := os.Stat(path)
			if !filepath.IsAbs(path) || err != nil || !info.Mode().IsRegular() {
				return AgentConfig{}, errors.New("proxy TLS certificate and key require absolute regular files")
			}
		}
		info, err := os.Stat(cfg.ProxyKeyFile)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			return AgentConfig{}, errors.New("proxy TLS private key must be readable only by owner")
		}
	}
	return cfg, nil
}
