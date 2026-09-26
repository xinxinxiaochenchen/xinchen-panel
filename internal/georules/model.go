package georules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

var codePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

type NewRuleSet struct {
	Kind    string   `json:"kind"`
	Code    string   `json:"code"`
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Source  string   `json:"source"`
	SHA256  string   `json:"sha256,omitempty"`
	Entries []string `json:"entries"`
	Enabled *bool    `json:"enabled,omitempty"`
}

type Input struct {
	Kind    string
	Code    string
	Name    string
	Version string
	Source  string
	SHA256  string
	Entries []string
	Enabled bool
}

type RuleSet struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	Version    string    `json:"version"`
	Source     string    `json:"source"`
	SHA256     string    `json:"sha256"`
	EntryCount int       `json:"entry_count"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Data struct {
	Kind    string
	Code    string
	Version string
	SHA256  string
	Entries []string
}

type ValidationError struct{ Field, Reason string }

func (e ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

func Normalize(in NewRuleSet) (Input, error) {
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind != "geosite" && kind != "geoip" {
		return Input{}, ValidationError{"kind", "expected geosite or geoip"}
	}
	code := strings.ToLower(strings.TrimSpace(in.Code))
	if !codePattern.MatchString(code) {
		return Input{}, ValidationError{"code", "expected 2 to 64 lowercase letters, digits, hyphens or underscores"}
	}
	name, version, source := strings.TrimSpace(in.Name), strings.TrimSpace(in.Version), strings.TrimSpace(in.Source)
	if name == "" || len([]rune(name)) > 100 {
		return Input{}, ValidationError{"name", "expected 1 to 100 characters"}
	}
	if version == "" || len([]rune(version)) > 64 || hasControl(version) {
		return Input{}, ValidationError{"version", "expected 1 to 64 printable characters"}
	}
	if source == "" || len([]rune(source)) > 500 || hasControl(source) {
		return Input{}, ValidationError{"source", "expected 1 to 500 printable characters"}
	}
	if len(in.Entries) == 0 || len(in.Entries) > 10000 {
		return Input{}, ValidationError{"entries", "expected 1 to 10000 entries"}
	}
	entries := make([]string, 0, len(in.Entries))
	seen := make(map[string]struct{}, len(in.Entries))
	for _, raw := range in.Entries {
		entry, err := normalizeEntry(kind, raw)
		if err != nil {
			return Input{}, ValidationError{"entries", err.Error()}
		}
		if _, ok := seen[entry]; ok {
			return Input{}, ValidationError{"entries", "entries must be unique"}
		}
		seen[entry] = struct{}{}
		entries = append(entries, entry)
	}
	slices.Sort(entries)
	canonical := strings.Join(entries, "\n")
	sum := sha256.Sum256([]byte(canonical))
	computed := hex.EncodeToString(sum[:])
	if in.SHA256 != "" && !strings.EqualFold(strings.TrimSpace(in.SHA256), computed) {
		return Input{}, ValidationError{"sha256", "does not match normalized entries"}
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return Input{Kind: kind, Code: code, Name: name, Version: version, Source: source, SHA256: computed, Entries: entries, Enabled: enabled}, nil
}

func normalizeEntry(kind, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("entry is empty")
	}
	if kind == "geoip" {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.IsValid() {
			return "", fmt.Errorf("invalid CIDR %q", value)
		}
		return prefix.Masked().String(), nil
	}
	prefix := "suffix:"
	if strings.HasPrefix(strings.ToLower(value), "domain:") {
		prefix, value = "domain:", value[len("domain:"):]
	} else if strings.HasPrefix(strings.ToLower(value), "suffix:") {
		value = value[len("suffix:"):]
	}
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if !validDomain(value) {
		return "", fmt.Errorf("invalid domain %q", value)
	}
	return prefix + value, nil
}

func validDomain(value string) bool {
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/\\,\r\n\t ") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
	}
	return true
}

func hasControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
