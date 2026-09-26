package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnrollmentConfigRequiresPinnedHTTPSAndNewCredentialPaths(t *testing.T) {
	directory := t.TempDir()
	caPath := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(caPath, []byte("CA"), 0644); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"CONTROL_AGENT_ENROLL_URL":   "https://127.0.0.1:18443/api/v1/agent/enroll",
		"CONTROL_AGENT_NODE_ID":      "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423",
		"CONTROL_AGENT_VERSION":      "1.0.0",
		"CONTROL_AGENT_CA_CERT_FILE": caPath,
		"CONTROL_AGENT_CERT_FILE":    filepath.Join(directory, "client.pem"),
		"CONTROL_AGENT_KEY_FILE":     filepath.Join(directory, "client.key"),
	}
	lookup := func(key string) (string, bool) { value, ok := values[key]; return value, ok }
	loaded, err := LoadEnrollmentConfig(lookup)
	if err != nil || loaded.URL != values["CONTROL_AGENT_ENROLL_URL"] {
		t.Fatalf("valid enrollment = %+v, %v", loaded, err)
	}
	values["CONTROL_AGENT_ENROLL_URL"] = "http://127.0.0.1:18443/api/v1/agent/enroll"
	if _, err := LoadEnrollmentConfig(lookup); err == nil {
		t.Fatal("plaintext enrollment accepted")
	}
	values["CONTROL_AGENT_ENROLL_URL"] = "https://127.0.0.1:18443/api/v1/agent/enroll"
	if err := os.WriteFile(values["CONTROL_AGENT_KEY_FILE"], []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEnrollmentConfig(lookup); err == nil {
		t.Fatal("existing credential path accepted")
	}
}
