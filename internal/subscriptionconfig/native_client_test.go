package subscriptionconfig

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Set CONTROL_TEST_MIHOMO_BINARY to validate generated YAML with a real
// Mihomo binary. The ordinary test suite remains independent of that tool.
func TestNativeMihomoAcceptsRenderedProfiles(t *testing.T) {
	binary := os.Getenv("CONTROL_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("CONTROL_TEST_MIHOMO_BINARY is not set")
	}
	target := target("Japan")
	target.LineID = "line-jp"
	policy := &RoutingPolicy{
		Fallback: Action{Kind: "direct"},
		Rules: []Rule{{MatchType: "domain_suffix", MatchValue: "example.com", Action: Action{Kind: "line", LineID: target.LineID}}},
	}
	for _, format := range []string{"clash", "mihomo"} {
		t.Run(format, func(t *testing.T) {
			body, _, err := RenderWithRouting(format, "{name}", []Target{target}, policy)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			config := filepath.Join(root, "config.yaml")
			if err := os.WriteFile(config, body, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "-t", "-f", config, "-d", root)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("%s configuration rejected by Mihomo: %v\n%s", format, err, output)
			}
		})
	}
}

// Set CONTROL_TEST_SING_BOX_BINARY to verify the emitted JSON against the
// supported sing-box 1.12 client. No network or live proxy is required.
func TestNativeSingBoxAcceptsRenderedProfiles(t *testing.T) {
	binary := os.Getenv("CONTROL_TEST_SING_BOX_BINARY")
	if binary == "" {
		t.Skip("CONTROL_TEST_SING_BOX_BINARY is not set")
	}
	target := target("Japan")
	target.LineID = "line-jp"
	cases := []struct {
		name   string
		policy *RoutingPolicy
	}{
		{name: "default"},
		{name: "routed", policy: &RoutingPolicy{
			Fallback: Action{Kind: "direct"},
			Rules: []Rule{{MatchType: "domain_suffix", MatchValue: "example.com", Action: Action{Kind: "line", LineID: target.LineID}}},
		}},
		{name: "block-fallback", policy: &RoutingPolicy{Fallback: Action{Kind: "block"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _, err := RenderWithRouting("sing-box", "{name}", []Target{target}, tc.policy)
			if err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(config, body, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "check", "-c", config)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("sing-box configuration rejected: %v\n%s", err, output)
			}
		})
	}
}
