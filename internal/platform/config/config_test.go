package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromDefaults(t *testing.T) {
	cfg, err := LoadFrom(func(key string) (string, bool) {
		if key == "CONTROL_DATABASE_URL" {
			return "postgres://app:secret@localhost:5432/control", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.LogLevel != slog.LevelInfo || cfg.DatabaseURL == "" || cfg.BrowserAuthEnabled {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadFromBrowserAuthExplicitFlag(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "proxy-key")
	if err := os.WriteFile(keyPath, []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), 0600); err != nil {
		t.Fatal(err)
	}
	lookup := func(key string) (string, bool) {
		values := map[string]string{"CONTROL_DATABASE_URL": "postgres://localhost/control", "CONTROL_BROWSER_AUTH_ENABLED": "true", "CONTROL_PROXY_CREDENTIAL_KEY_FILE": keyPath}
		value, ok := values[key]
		return value, ok
	}
	cfg, err := LoadFrom(lookup)
	if err != nil || !cfg.BrowserAuthEnabled {
		t.Fatalf("enabled flag = %+v, %v", cfg, err)
	}
	_, err = LoadFrom(func(key string) (string, bool) {
		if key == "CONTROL_BROWSER_AUTH_ENABLED" {
			return "sometimes", true
		}
		return lookup(key)
	})
	if err == nil {
		t.Fatal("expected invalid boolean error")
	}
}

func TestLoadFromOptionalWebDirectory(t *testing.T) {
	cfg, err := LoadFrom(func(key string) (string, bool) {
		values := map[string]string{"CONTROL_DATABASE_URL": "postgres://localhost/control", "CONTROL_WEB_DIR": "/app/web"}
		value, ok := values[key]
		return value, ok
	})
	if err != nil || cfg.WebDir != "/app/web" {
		t.Fatalf("web directory = %+v, %v", cfg, err)
	}
	_, err = LoadFrom(func(key string) (string, bool) {
		if key == "CONTROL_WEB_DIR" {
			return "", true
		}
		if key == "CONTROL_DATABASE_URL" {
			return "postgres://localhost/control", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("accepted an explicitly empty web directory")
	}
}

func TestLoadFromRejectsInvalidAddress(t *testing.T) {
	_, err := LoadFrom(func(key string) (string, bool) {
		if key == "CONTROL_HTTP_ADDR" {
			return "not-an-address", true
		}
		if key == "CONTROL_DATABASE_URL" {
			return "postgres://localhost/control", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("expected invalid address error")
	}
}

func TestLoadFromRejectsInvalidLogLevel(t *testing.T) {
	_, err := LoadFrom(func(key string) (string, bool) {
		if key == "CONTROL_LOG_LEVEL" {
			return "verbose", true
		}
		if key == "CONTROL_DATABASE_URL" {
			return "postgres://localhost/control", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("expected invalid log level error")
	}
}

func TestLoadFromOverrides(t *testing.T) {
	cfg, err := LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"CONTROL_HTTP_ADDR":    ":9090",
			"CONTROL_LOG_LEVEL":    "debug",
			"CONTROL_DATABASE_URL": "postgres://localhost/control",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9090" || cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected overrides: %+v", cfg)
	}
}

func TestLoadFromRequiresDatabaseURL(t *testing.T) {
	_, err := LoadFrom(func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("expected missing database URL error")
	}
}

func TestLoadFromRejectsNonPostgresURL(t *testing.T) {
	_, err := LoadFrom(func(key string) (string, bool) {
		if key == "CONTROL_DATABASE_URL" {
			return "https://example.com/control", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("expected invalid database URL error")
	}
}

func TestLoadFromAgentTLSRequiresCompleteFiles(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"server.crt", "server.key", "agent-ca.crt", "agent-ca.key"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	values := map[string]string{"CONTROL_DATABASE_URL": "postgres://localhost/control", "CONTROL_AGENT_TLS_ADDR": "127.0.0.1:18443",
		"CONTROL_AGENT_TLS_CERT_FILE": filepath.Join(directory, "server.crt"), "CONTROL_AGENT_TLS_KEY_FILE": filepath.Join(directory, "server.key"),
		"CONTROL_AGENT_CA_CERT_FILE": filepath.Join(directory, "agent-ca.crt"), "CONTROL_AGENT_CA_KEY_FILE": filepath.Join(directory, "agent-ca.key")}
	lookup := func(key string) (string, bool) { value, ok := values[key]; return value, ok }
	cfg, err := LoadFrom(lookup)
	if err != nil || cfg.AgentTLSAddr != "127.0.0.1:18443" || cfg.AgentCACertFile == "" {
		t.Fatalf("complete Agent TLS config = %+v, %v", cfg, err)
	}
	delete(values, "CONTROL_AGENT_CA_KEY_FILE")
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("incomplete Agent TLS config accepted")
	}
}

func TestLoadFromAgentTLSRejectsPublicBindAndWeakKeyPermissions(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"server.crt", "server.key", "agent-ca.crt", "agent-ca.key"} {
		mode := os.FileMode(0600)
		if name == "agent-ca.key" {
			mode = 0644
		}
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), mode); err != nil {
			t.Fatal(err)
		}
	}
	values := map[string]string{"CONTROL_DATABASE_URL": "postgres://localhost/control", "CONTROL_AGENT_TLS_ADDR": "127.0.0.1:18443",
		"CONTROL_AGENT_TLS_CERT_FILE": filepath.Join(directory, "server.crt"), "CONTROL_AGENT_TLS_KEY_FILE": filepath.Join(directory, "server.key"),
		"CONTROL_AGENT_CA_CERT_FILE": filepath.Join(directory, "agent-ca.crt"), "CONTROL_AGENT_CA_KEY_FILE": filepath.Join(directory, "agent-ca.key")}
	lookup := func(key string) (string, bool) { value, ok := values[key]; return value, ok }
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("world-readable Agent CA key accepted")
	}
	if err := os.Chmod(values["CONTROL_AGENT_CA_KEY_FILE"], 0600); err != nil {
		t.Fatal(err)
	}
	values["CONTROL_AGENT_TLS_ADDR"] = "0.0.0.0:18443"
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("public Agent TLS bind accepted")
	}
}

func TestLoadFromBrowserAuthRequiresProxyCredentialKey(t *testing.T) {
	values := map[string]string{"CONTROL_DATABASE_URL": "postgres://localhost/control", "CONTROL_BROWSER_AUTH_ENABLED": "true"}
	lookup := func(key string) (string, bool) { value, ok := values[key]; return value, ok }
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("browser auth accepted without credential encryption key")
	}
	keyPath := filepath.Join(t.TempDir(), "proxy-key")
	if err := os.WriteFile(keyPath, []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	values["CONTROL_PROXY_CREDENTIAL_KEY_FILE"] = keyPath
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("browser auth accepted short credential encryption key")
	}
	if err := os.WriteFile(keyPath, []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(lookup)
	if err != nil || len(cfg.ProxyCredentialKey) != 32 {
		t.Fatalf("valid credential key = %v, length=%d", err, len(cfg.ProxyCredentialKey))
	}
}

func TestLoadFromAgentTLSPublicBindNeedsExplicitOptIn(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"server.crt", "server.key", "agent-ca.crt", "agent-ca.key"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	values := map[string]string{
		"CONTROL_DATABASE_URL":        "postgres://localhost/control",
		"CONTROL_AGENT_TLS_ADDR":      "0.0.0.0:18443",
		"CONTROL_AGENT_TLS_CERT_FILE": filepath.Join(directory, "server.crt"),
		"CONTROL_AGENT_TLS_KEY_FILE":  filepath.Join(directory, "server.key"),
		"CONTROL_AGENT_CA_CERT_FILE":  filepath.Join(directory, "agent-ca.crt"),
		"CONTROL_AGENT_CA_KEY_FILE":   filepath.Join(directory, "agent-ca.key"),
	}
	lookup := func(key string) (string, bool) { value, ok := values[key]; return value, ok }
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("public Agent TLS bind accepted without explicit opt-in")
	}
	values["CONTROL_AGENT_PUBLIC_TLS_ENABLED"] = "true"
	cfg, err := LoadFrom(lookup)
	if err != nil || cfg.AgentTLSAddr != "0.0.0.0:18443" {
		t.Fatalf("explicit public Agent TLS bind = %+v, %v", cfg, err)
	}
	for _, addr := range []string{"179.255.145.149:18443", "[2001:db8::1]:18443"} {
		values["CONTROL_AGENT_TLS_ADDR"] = addr
		if _, err := LoadFrom(lookup); err != nil {
			t.Fatalf("explicit IP listener %q rejected: %v", addr, err)
		}
	}
	values["CONTROL_AGENT_PUBLIC_TLS_ENABLED"] = "false"
	values["CONTROL_AGENT_TLS_ADDR"] = "0.0.0.0:18443"
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("false public TLS flag accepted wildcard listener")
	}
	values["CONTROL_AGENT_PUBLIC_TLS_ENABLED"] = "true"
	if err := os.Chmod(values["CONTROL_AGENT_TLS_KEY_FILE"], 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("public TLS listener accepted weak server key permissions")
	}
	if err := os.Chmod(values["CONTROL_AGENT_TLS_KEY_FILE"], 0600); err != nil {
		t.Fatal(err)
	}
	values["CONTROL_AGENT_PUBLIC_TLS_ENABLED"] = "maybe"
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("malformed public TLS flag accepted")
	}
	values["CONTROL_AGENT_PUBLIC_TLS_ENABLED"] = "true"
	values["CONTROL_AGENT_TLS_ADDR"] = "agent.example.test:18443"
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("hostname Agent TLS bind accepted")
	}
	delete(values, "CONTROL_AGENT_TLS_ADDR")
	if _, err := LoadFrom(lookup); err == nil {
		t.Fatal("public TLS opt-in accepted without Agent TLS listener")
	}
}
