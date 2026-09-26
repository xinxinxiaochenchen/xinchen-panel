package main

import "testing"

func TestLoadTokenConfigRequiresDatabaseAdminAndNode(t *testing.T) {
	values := map[string]string{"CONTROL_DATABASE_URL": "postgres://app:secret@db:5432/controlplane",
		"CONTROL_ADMIN_EMAIL": "admin@example.invalid", "CONTROL_AGENT_TOKEN_OUTPUT_FILE": "/tmp/enrollment-token.json"}
	lookup := func(key string) (string, bool) { value, ok := values[key]; return value, ok }
	validNode := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
	cfg, err := LoadTokenConfig(lookup, []string{validNode})
	if err != nil || cfg.NodeID != validNode {
		t.Fatalf("valid token config = %+v, %v", cfg, err)
	}
	if _, err := LoadTokenConfig(lookup, nil); err == nil {
		t.Fatal("missing node ID accepted")
	}
	if _, err := LoadTokenConfig(lookup, []string{"not-a-uuid"}); err == nil {
		t.Fatal("invalid node ID accepted")
	}
	delete(values, "CONTROL_ADMIN_EMAIL")
	if _, err := LoadTokenConfig(lookup, []string{validNode}); err == nil {
		t.Fatal("missing administrator accepted")
	}
}
