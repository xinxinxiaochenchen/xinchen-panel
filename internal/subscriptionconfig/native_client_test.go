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
