package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr           string
	LogLevel           slog.Level
	DatabaseURL        string
	BrowserAuthEnabled bool
}

func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{HTTPAddr: "127.0.0.1:8080", LogLevel: slog.LevelInfo}
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
	if value, ok := lookup("CONTROL_BROWSER_AUTH_ENABLED"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("CONTROL_BROWSER_AUTH_ENABLED: expected true or false")
		}
		cfg.BrowserAuthEnabled = enabled
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
	return cfg, nil
}
