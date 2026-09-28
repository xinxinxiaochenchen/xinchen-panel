package subscription

import (
	"controlplane/internal/catalog"
	"controlplane/internal/subscriptionconfig"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrNotFound    = errors.New("subscription not found")
	ErrConflict    = errors.New("subscription conflict")
	ErrLimit       = errors.New("subscription limit exceeded")
	ErrUnavailable = errors.New("subscription has no available proxy accesses")
)

type ValidationError struct{ Field, Reason string }

func (e ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

type NewSubscription struct {
	Name           string   `json:"name"`
	NameTemplate   string   `json:"name_template"`
	ProxyAccessIDs []string `json:"proxy_access_ids"`
	Enabled        *bool    `json:"enabled,omitempty"`
}
type Input struct {
	Name, NameTemplate string
	ProxyAccessIDs     []string
	Enabled            bool
}
type Patch struct {
	Name           *string   `json:"name,omitempty"`
	NameTemplate   *string   `json:"name_template,omitempty"`
	ProxyAccessIDs *[]string `json:"proxy_access_ids,omitempty"`
	Enabled        *bool     `json:"enabled,omitempty"`
}

type Subscription struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Name           string    `json:"name"`
	NameTemplate   string    `json:"name_template"`
	ProxyAccessIDs []string  `json:"proxy_access_ids"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func Normalize(in NewSubscription) (Input, error) { return normalize(in, false) }
func normalize(in NewSubscription, allowEmpty bool) (Input, error) {
	if !utf8.ValidString(in.Name) || strings.ContainsFunc(in.Name, unicode.IsControl) {
		return Input{}, ValidationError{"name", "invalid characters"}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return Input{}, ValidationError{"name", "expected 1 to 100 characters"}
	}
	template := in.NameTemplate
	if template == "" {
		template = "{region} · {name}"
	}
	if err := subscriptionconfig.ValidateTemplate(template); err != nil {
		return Input{}, ValidationError{"name_template", err.Error()}
	}
	if (!allowEmpty && len(in.ProxyAccessIDs) < 1) || len(in.ProxyAccessIDs) > 100 {
		return Input{}, ValidationError{"proxy_access_ids", "expected 1 to 100 targets"}
	}
	targets := make([]string, len(in.ProxyAccessIDs))
	seen := map[string]bool{}
	for i, v := range in.ProxyAccessIDs {
		v = strings.ToLower(v)
		if !catalog.ValidID(v) || seen[v] {
			return Input{}, ValidationError{"proxy_access_ids", "expected unique UUIDs"}
		}
		seen[v] = true
		targets[i] = v
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return Input{Name: name, NameTemplate: template, ProxyAccessIDs: targets, Enabled: enabled}, nil
}
