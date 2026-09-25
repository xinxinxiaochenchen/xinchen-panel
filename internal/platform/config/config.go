package config

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr string
	LogLevel slog.Level
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
