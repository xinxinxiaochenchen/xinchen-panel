package main

import "testing"

func TestLoadBootstrapConfig(t *testing.T) {
	values := map[string]string{
		"CONTROL_DATABASE_URL":      "postgres://app:secret@db:5432/controlplane",
		"CONTROL_ADMIN_EMAIL":       "Admin@Example.Invalid",
		"CONTROL_NODE_GROUP_CODE":   "RFC.USDMIT",
		"CONTROL_NODE_GROUP_NAME":   "US Dmit",
		"CONTROL_NODE_GROUP_REGION": "US",
		"CONTROL_NODE_NAME":         "us-dmit-local",
		"CONTROL_NODE_REGION":       "US",
		"CONTROL_NODE_HOST":         "127.0.0.1",
		"CONTROL_NODE_CAPABILITIES": "forward,proxy",
		"CONTROL_NODE_PUBLIC_IP":    "179.255.145.149",
		"CONTROL_NODE_PROXY_PORT":   "18444",
		"CONTROL_NODE_RELAY_PORT":   "24443",
	}
	lookup := func(key string) (string, bool) { value, ok := values[key]; return value, ok }
	cfg, err := LoadBootstrapConfig(lookup)
	if err != nil || cfg.AdminEmail != "admin@example.invalid" || len(cfg.Capabilities) != 2 || cfg.ProxyPort == nil || *cfg.ProxyPort != 18444 || cfg.RelayPort == nil || *cfg.RelayPort != 24443 {
		t.Fatalf("config = %+v, err=%v", cfg, err)
	}
	for _, key := range []string{"CONTROL_DATABASE_URL", "CONTROL_ADMIN_EMAIL", "CONTROL_NODE_GROUP_CODE", "CONTROL_NODE_NAME", "CONTROL_NODE_HOST", "CONTROL_NODE_CAPABILITIES"} {
		original := values[key]
		delete(values, key)
		if _, err := LoadBootstrapConfig(lookup); err == nil {
			t.Fatalf("missing %s accepted", key)
		}
		values[key] = original
	}
	values["CONTROL_NODE_CAPABILITIES"] = "forward,forward"
	if _, err := LoadBootstrapConfig(lookup); err == nil {
		t.Fatal("duplicate capability accepted")
	}
}
