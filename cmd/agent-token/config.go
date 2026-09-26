package main

import (
	"errors"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var nodeIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type TokenConfig struct {
	DatabaseURL string
	AdminEmail  string
	NodeID      string
	OutputFile  string
}

func LoadTokenConfig(lookup func(string) (string, bool), args []string) (TokenConfig, error) {
	if len(args) != 1 || !nodeIDPattern.MatchString(args[0]) {
		return TokenConfig{}, errors.New("one node UUID is required")
	}
	databaseURL, ok := lookup("CONTROL_DATABASE_URL")
	if !ok {
		return TokenConfig{}, errors.New("CONTROL_DATABASE_URL is required")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return TokenConfig{}, errors.New("CONTROL_DATABASE_URL must be a PostgreSQL URL")
	}
	adminEmail, ok := lookup("CONTROL_ADMIN_EMAIL")
	if !ok || !strings.Contains(adminEmail, "@") {
		return TokenConfig{}, errors.New("CONTROL_ADMIN_EMAIL is required")
	}
	output, ok := lookup("CONTROL_AGENT_TOKEN_OUTPUT_FILE")
	if !ok || !filepath.IsAbs(output) {
		return TokenConfig{}, errors.New("CONTROL_AGENT_TOKEN_OUTPUT_FILE must be absolute")
	}
	return TokenConfig{DatabaseURL: databaseURL, AdminEmail: strings.ToLower(strings.TrimSpace(adminEmail)), NodeID: args[0], OutputFile: output}, nil
}
