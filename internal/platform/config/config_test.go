package config

import (
	"log/slog"
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
	lookup := func(key string) (string, bool) {
		values := map[string]string{"CONTROL_DATABASE_URL": "postgres://localhost/control", "CONTROL_BROWSER_AUTH_ENABLED": "true"}
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
