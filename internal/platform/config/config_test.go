package config

import (
	"log/slog"
	"testing"
)

func TestLoadFromDefaults(t *testing.T) {
	cfg, err := LoadFrom(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8080" || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadFromRejectsInvalidAddress(t *testing.T) {
	_, err := LoadFrom(func(key string) (string, bool) {
		if key == "CONTROL_HTTP_ADDR" {
			return "not-an-address", true
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
		return "", false
	})
	if err == nil {
		t.Fatal("expected invalid log level error")
	}
}

func TestLoadFromOverrides(t *testing.T) {
	cfg, err := LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"CONTROL_HTTP_ADDR": ":9090",
			"CONTROL_LOG_LEVEL": "debug",
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
