package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupTokenRequiresPrivateCanonicalRandomFile(t *testing.T) {
	for _, tc := range []struct {
		name, contents string
		mode           os.FileMode
		valid          bool
	}{
		{"private", strings.Repeat("A", 43) + "\n", 0600, true},
		{"public", strings.Repeat("A", 43), 0644, false},
		{"short", "short", 0600, false},
		{"noncanonical", strings.Repeat("A", 42) + "B", 0600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "setup.token")
			if err := os.WriteFile(path, []byte(tc.contents), tc.mode); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFrom(func(key string) (string, bool) {
				values := map[string]string{"CONTROL_DATABASE_URL": "postgres://localhost/control", "CONTROL_SETUP_TOKEN_FILE": path}
				value, ok := values[key]
				return value, ok
			})
			if tc.valid {
				if err != nil || cfg.SetupToken != strings.TrimSpace(tc.contents) {
					t.Fatalf("token was not loaded: %v", err)
				}
			} else if err == nil {
				t.Fatal("unsafe setup file accepted")
			}
		})
	}
}
