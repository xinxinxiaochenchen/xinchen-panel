package main

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type BootstrapConfig struct {
	DatabaseURL  string
	AdminEmail   string
	GroupCode    string
	GroupName    string
	GroupRegion  string
	NodeName     string
	NodeRegion   string
	NodeHost     string
	PublicIP     string
	Capabilities []string
	ProxyPort    *int
	RelayPort    *int
}

func LoadBootstrapConfig(lookup func(string) (string, bool)) (BootstrapConfig, error) {
	get := func(key string) (string, error) {
		value, ok := lookup(key)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			return "", errors.New(key + " is required")
		}
		return value, nil
	}
	databaseURL, err := get("CONTROL_DATABASE_URL")
	if err != nil {
		return BootstrapConfig{}, err
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return BootstrapConfig{}, errors.New("CONTROL_DATABASE_URL must be a PostgreSQL URL")
	}
	adminEmail, err := get("CONTROL_ADMIN_EMAIL")
	if err != nil {
		return BootstrapConfig{}, err
	}
	groupCode, err := get("CONTROL_NODE_GROUP_CODE")
	if err != nil {
		return BootstrapConfig{}, err
	}
	groupName, err := get("CONTROL_NODE_GROUP_NAME")
	if err != nil {
		return BootstrapConfig{}, err
	}
	groupRegion, err := get("CONTROL_NODE_GROUP_REGION")
	if err != nil {
		return BootstrapConfig{}, err
	}
	nodeName, err := get("CONTROL_NODE_NAME")
	if err != nil {
		return BootstrapConfig{}, err
	}
	nodeRegion, err := get("CONTROL_NODE_REGION")
	if err != nil {
		return BootstrapConfig{}, err
	}
	nodeHost, err := get("CONTROL_NODE_HOST")
	if err != nil {
		return BootstrapConfig{}, err
	}
	capabilityText, err := get("CONTROL_NODE_CAPABILITIES")
	if err != nil {
		return BootstrapConfig{}, err
	}
	var capabilities []string
	seen := map[string]struct{}{}
	for _, value := range strings.Split(capabilityText, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			return BootstrapConfig{}, errors.New("CONTROL_NODE_CAPABILITIES contains an empty value")
		}
		if _, ok := seen[value]; ok {
			return BootstrapConfig{}, errors.New("CONTROL_NODE_CAPABILITIES contains duplicates")
		}
		seen[value] = struct{}{}
		capabilities = append(capabilities, value)
	}
	cfg := BootstrapConfig{DatabaseURL: databaseURL, AdminEmail: strings.ToLower(adminEmail), GroupCode: groupCode,
		GroupName: groupName, GroupRegion: groupRegion, NodeName: nodeName, NodeRegion: nodeRegion,
		NodeHost: nodeHost, Capabilities: capabilities}
	if value, ok := lookup("CONTROL_NODE_PUBLIC_IP"); ok {
		cfg.PublicIP = strings.TrimSpace(value)
	}
	if value, ok := lookup("CONTROL_NODE_PROXY_PORT"); ok && strings.TrimSpace(value) != "" {
		port, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return BootstrapConfig{}, errors.New("CONTROL_NODE_PROXY_PORT must be an integer")
		}
		cfg.ProxyPort = &port
	}
	if value, ok := lookup("CONTROL_NODE_RELAY_PORT"); ok && strings.TrimSpace(value) != "" {
		port, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return BootstrapConfig{}, errors.New("CONTROL_NODE_RELAY_PORT must be an integer")
		}
		cfg.RelayPort = &port
	}
	return cfg, nil
}
