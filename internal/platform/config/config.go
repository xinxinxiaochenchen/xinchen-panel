package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr              string
	LogLevel              slog.Level
	DatabaseURL           string
	BrowserAuthEnabled    bool
	BrowserCookieSecure   bool
	ProxyCredentialKey    []byte
	RelaySecretKey        []byte
	WebDir                string
	AgentTLSAddr          string
	AgentPublicTLSEnabled bool
	AgentTLSCertFile      string
	AgentTLSKeyFile       string
	AgentCACertFile       string
	AgentCAKeyFile        string
}

func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{HTTPAddr: "127.0.0.1:8080", LogLevel: slog.LevelInfo, BrowserCookieSecure: true}
	if value, ok := lookup("CONTROL_HTTP_ADDR"); ok {
		cfg.HTTPAddr = value
	}
	_, portText, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return Config{}, fmt.Errorf("CONTROL_HTTP_ADDR: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("CONTROL_HTTP_ADDR: invalid port %q", portText)
	}
	value, ok := lookup("CONTROL_DATABASE_URL")
	if !ok || value == "" {
		return Config{}, fmt.Errorf("CONTROL_DATABASE_URL: required")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return Config{}, fmt.Errorf("CONTROL_DATABASE_URL: expected a postgres URL with host")
	}
	cfg.DatabaseURL = value
	if value, ok := lookup("CONTROL_WEB_DIR"); ok {
		if value == "" || !filepath.IsAbs(value) {
			return Config{}, fmt.Errorf("CONTROL_WEB_DIR: expected an absolute directory path")
		}
		cfg.WebDir = value
	}
	if value, ok := lookup("CONTROL_BROWSER_AUTH_ENABLED"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("CONTROL_BROWSER_AUTH_ENABLED: expected true or false")
		}
		cfg.BrowserAuthEnabled = enabled
	}
	if value, ok := lookup("CONTROL_BROWSER_COOKIE_SECURE"); ok {
		secure, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("CONTROL_BROWSER_COOKIE_SECURE: expected true or false")
		}
		cfg.BrowserCookieSecure = secure
	}
	if cfg.BrowserAuthEnabled {
		path, ok := lookup("CONTROL_PROXY_CREDENTIAL_KEY_FILE")
		if !ok || !filepath.IsAbs(path) {
			return Config{}, fmt.Errorf("CONTROL_PROXY_CREDENTIAL_KEY_FILE: absolute file path required when browser authentication is enabled")
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return Config{}, fmt.Errorf("CONTROL_PROXY_CREDENTIAL_KEY_FILE: private regular file required")
		}
		encoded, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("CONTROL_PROXY_CREDENTIAL_KEY_FILE: unreadable: %w", err)
		}
		keyText := strings.TrimSpace(string(encoded))
		decoded, err := base64.RawURLEncoding.DecodeString(keyText)
		if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != keyText {
			return Config{}, fmt.Errorf("CONTROL_PROXY_CREDENTIAL_KEY_FILE: expected 32 base64url-encoded bytes")
		}
		cfg.ProxyCredentialKey = decoded
	}
	if path, ok := lookup("CONTROL_RELAY_SECRET_KEY_FILE"); ok {
		if !filepath.IsAbs(path) {
			return Config{}, fmt.Errorf("CONTROL_RELAY_SECRET_KEY_FILE: absolute file path required")
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return Config{}, fmt.Errorf("CONTROL_RELAY_SECRET_KEY_FILE: private regular file required")
		}
		encoded, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("CONTROL_RELAY_SECRET_KEY_FILE: unreadable: %w", err)
		}
		keyText := strings.TrimSpace(string(encoded))
		decoded, err := base64.RawURLEncoding.DecodeString(keyText)
		if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != keyText {
			return Config{}, fmt.Errorf("CONTROL_RELAY_SECRET_KEY_FILE: expected 32 base64url-encoded bytes")
		}
		cfg.RelaySecretKey = decoded
	}
	if value, ok := lookup("CONTROL_LOG_LEVEL"); ok {
		switch strings.ToLower(value) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			return Config{}, fmt.Errorf("CONTROL_LOG_LEVEL: unsupported level %q", value)
		}
	}
	if value, ok := lookup("CONTROL_AGENT_PUBLIC_TLS_ENABLED"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("CONTROL_AGENT_PUBLIC_TLS_ENABLED: expected true or false")
		}
		cfg.AgentPublicTLSEnabled = enabled
	}
	agentFields := []struct {
		key    string
		target *string
	}{
		{"CONTROL_AGENT_TLS_ADDR", &cfg.AgentTLSAddr},
		{"CONTROL_AGENT_TLS_CERT_FILE", &cfg.AgentTLSCertFile},
		{"CONTROL_AGENT_TLS_KEY_FILE", &cfg.AgentTLSKeyFile},
		{"CONTROL_AGENT_CA_CERT_FILE", &cfg.AgentCACertFile},
		{"CONTROL_AGENT_CA_KEY_FILE", &cfg.AgentCAKeyFile},
	}
	agentConfigured := false
	for _, field := range agentFields {
		if value, ok := lookup(field.key); ok && value != "" {
			*field.target = value
			agentConfigured = true
		}
	}
	if cfg.AgentPublicTLSEnabled && !agentConfigured {
		return Config{}, fmt.Errorf("CONTROL_AGENT_PUBLIC_TLS_ENABLED: Agent TLS listener is required")
	}
	if agentConfigured {
		for _, field := range agentFields {
			if *field.target == "" {
				return Config{}, fmt.Errorf("%s: required when Agent TLS is enabled", field.key)
			}
		}
		host, portText, err := net.SplitHostPort(cfg.AgentTLSAddr)
		port, portErr := strconv.Atoi(portText)
		loopback := host == "127.0.0.1" || host == "::1" || host == "localhost"
		publicIP := cfg.AgentPublicTLSEnabled && net.ParseIP(host) != nil
		if err != nil || portErr != nil || port < 1 || port > 65535 || !loopback && !publicIP {
			return Config{}, fmt.Errorf("CONTROL_AGENT_TLS_ADDR: expected a loopback address or explicitly enabled IP listener and valid port")
		}
		for _, field := range agentFields[1:] {
			if !filepath.IsAbs(*field.target) {
				return Config{}, fmt.Errorf("%s: expected absolute file path", field.key)
			}
			info, err := os.Stat(*field.target)
			if err != nil || !info.Mode().IsRegular() {
				return Config{}, fmt.Errorf("%s: file unavailable", field.key)
			}
		}
		for _, field := range []struct {
			key  string
			path string
		}{{"CONTROL_AGENT_TLS_KEY_FILE", cfg.AgentTLSKeyFile}, {"CONTROL_AGENT_CA_KEY_FILE", cfg.AgentCAKeyFile}} {
			info, err := os.Stat(field.path)
			if err != nil || info.Mode().Perm()&0077 != 0 {
				return Config{}, fmt.Errorf("%s: private key must be readable only by its owner", field.key)
			}
		}
	}
	return cfg, nil
}
