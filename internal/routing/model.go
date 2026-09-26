package routing

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"controlplane/internal/catalog"
)

var domainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

type ValidationError struct{ Field, Reason string }

func (e ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

type NewProfile struct {
	Name           string  `json:"name"`
	FallbackKind   string  `json:"fallback_kind"`
	FallbackLineID *string `json:"fallback_line_id,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty"`
}

type ProfileInput struct {
	Name           string
	FallbackKind   string
	FallbackLineID *string
	Enabled        bool
}

type ProfilePatch struct {
	Name           *string `json:"name,omitempty"`
	FallbackKind   *string `json:"fallback_kind,omitempty"`
	FallbackLineID *string `json:"fallback_line_id,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty"`
}

type Profile struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Name           string    `json:"name"`
	FallbackKind   string    `json:"fallback_kind"`
	FallbackLineID *string   `json:"fallback_line_id"`
	Enabled        bool      `json:"enabled"`
	Revision       int64     `json:"revision"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type NewRule struct {
	Priority   int     `json:"priority"`
	MatchType  string  `json:"match_type"`
	MatchValue string  `json:"match_value"`
	Action     string  `json:"action"`
	LineID     *string `json:"line_id,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
}

type RuleInput struct {
	Priority   int
	MatchType  string
	MatchValue string
	Action     string
	LineID     *string
	Enabled    bool
}

type Rule struct {
	ID         string    `json:"id"`
	ProfileID  string    `json:"profile_id"`
	Priority   int       `json:"priority"`
	MatchType  string    `json:"match_type"`
	MatchValue string    `json:"match_value"`
	Action     string    `json:"action"`
	LineID     *string   `json:"line_id"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type RulePatch struct {
	Priority *int    `json:"priority,omitempty"`
	Action   *string `json:"action,omitempty"`
	LineID   *string `json:"line_id,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
}

func NormalizeProfile(in NewProfile) (ProfileInput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len([]rune(name)) > 100 {
		return ProfileInput{}, ValidationError{"name", "expected 1 to 100 characters"}
	}
	if in.FallbackKind != "direct" && in.FallbackKind != "block" && in.FallbackKind != "line" {
		return ProfileInput{}, ValidationError{"fallback_kind", "expected direct, block or line"}
	}
	var line *string
	if in.FallbackKind == "line" {
		if in.FallbackLineID == nil || !catalog.ValidID(*in.FallbackLineID) {
			return ProfileInput{}, ValidationError{"fallback_line_id", "required UUID for line fallback"}
		}
		v := strings.ToLower(*in.FallbackLineID)
		line = &v
	} else if in.FallbackLineID != nil {
		return ProfileInput{}, ValidationError{"fallback_line_id", "only permitted for line fallback"}
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return ProfileInput{Name: name, FallbackKind: in.FallbackKind, FallbackLineID: line, Enabled: enabled}, nil
}

func NormalizeRule(in NewRule) (RuleInput, error) {
	if in.Priority < 1 || in.Priority > 1000000 {
		return RuleInput{}, ValidationError{"priority", "expected 1 to 1000000"}
	}
	kind := strings.ToLower(strings.TrimSpace(in.MatchType))
	value, err := normalizeMatch(kind, in.MatchValue)
	if err != nil {
		return RuleInput{}, err
	}
	if in.Action != "direct" && in.Action != "block" && in.Action != "line" {
		return RuleInput{}, ValidationError{"action", "expected direct, block or line"}
	}
	var line *string
	if in.Action == "line" {
		if in.LineID == nil || !catalog.ValidID(*in.LineID) {
			return RuleInput{}, ValidationError{"line_id", "required UUID for line action"}
		}
		v := strings.ToLower(*in.LineID)
		line = &v
	} else if in.LineID != nil {
		return RuleInput{}, ValidationError{"line_id", "only permitted for line action"}
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return RuleInput{Priority: in.Priority, MatchType: kind, MatchValue: value, Action: in.Action, LineID: line, Enabled: enabled}, nil
}

func normalizeMatch(kind, raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", ValidationError{"match_value", "required"}
	}
	switch kind {
	case "domain", "domain_suffix":
		v = strings.TrimSuffix(v, ".")
		if kind == "domain_suffix" {
			v = strings.TrimPrefix(v, ".")
		}
		if len(v) > 253 || strings.Contains(v, "/") {
			return "", ValidationError{"match_value", "invalid domain"}
		}
		for _, l := range strings.Split(v, ".") {
			if !domainLabel.MatchString(l) {
				return "", ValidationError{"match_value", "invalid domain"}
			}
		}
	case "ip":
		a, err := netip.ParseAddr(v)
		if err != nil || !a.IsValid() {
			return "", ValidationError{"match_value", "invalid IP address"}
		}
		v = a.String()
	case "cidr":
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return "", ValidationError{"match_value", "invalid CIDR"}
		}
		v = p.Masked().String()
	case "geoip", "geosite":
		if len(v) < 2 || len(v) > 64 || !regexp.MustCompile(`^[a-z0-9_-]+$`).MatchString(v) {
			return "", ValidationError{"match_value", "invalid geo rule"}
		}
	default:
		return "", ValidationError{"match_type", "expected domain, domain_suffix, ip, cidr, geoip or geosite"}
	}
	return v, nil
}
