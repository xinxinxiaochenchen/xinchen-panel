package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAgentConfigRequiresSecurePathsAndWSS(t *testing.T) {
	directory := t.TempDir()
	write := func(name string, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte("test"), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	values := map[string]string{
		"CONTROL_AGENT_STREAM_URL":   "wss://127.0.0.1:18443/api/v1/agent/stream",
		"CONTROL_AGENT_NODE_ID":      "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423",
		"CONTROL_AGENT_VERSION":      "1.0.0",
		"CONTROL_AGENT_CA_CERT_FILE": write("ca.pem", 0644),
		"CONTROL_AGENT_CERT_FILE":    write("client.pem", 0644),
		"CONTROL_AGENT_KEY_FILE":     write("client.key", 0600),
		"CONTROL_AGENT_STATE_FILE":   filepath.Join(directory, "state.json"),
	}
	lookup := func(key string) (string, bool) { value, ok := values[key]; return value, ok }
	loaded, err := LoadAgentConfig(lookup)
	if err != nil || loaded.NodeID != values["CONTROL_AGENT_NODE_ID"] || loaded.BindHost != "0.0.0.0" {
		t.Fatalf("valid config = %+v, %v", loaded, err)
	}
	values["CONTROL_AGENT_PROXY_CERT_FILE"] = write("proxy.pem", 0644)
	values["CONTROL_AGENT_PROXY_KEY_FILE"] = write("proxy.key", 0600)
	loaded, err = LoadAgentConfig(lookup)
	if err != nil || loaded.ProxyCertFile != values["CONTROL_AGENT_PROXY_CERT_FILE"] {
		t.Fatalf("valid proxy TLS config = %+v, %v", loaded, err)
	}
	if err := os.Chmod(values["CONTROL_AGENT_PROXY_KEY_FILE"], 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAgentConfig(lookup); err == nil {
		t.Fatal("world-readable proxy private key accepted")
	}
	delete(values, "CONTROL_AGENT_PROXY_CERT_FILE")
	delete(values, "CONTROL_AGENT_PROXY_KEY_FILE")
	values["CONTROL_AGENT_STREAM_URL"] = "ws://127.0.0.1:18443/api/v1/agent/stream"
	if _, err := LoadAgentConfig(lookup); err == nil {
		t.Fatal("plaintext Agent stream accepted")
	}
	values["CONTROL_AGENT_STREAM_URL"] = "wss://127.0.0.1:18443/api/v1/agent/stream"
	if err := os.Chmod(values["CONTROL_AGENT_KEY_FILE"], 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAgentConfig(lookup); err == nil {
		t.Fatal("world-readable Agent private key accepted")
	}
}
