package proxyaccess

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var (
	ErrNotFound     = errors.New("proxy access not found")
	ErrConflict     = errors.New("proxy access conflict")
	ErrUnauthorized = errors.New("proxy access line is not authorized")
)

type ValidationError struct {
	Field  string
	Reason string
}

func (e ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

type NewAccess struct {
	Name    string `json:"name"`
	LineID  string `json:"line_id"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type AccessPatch struct {
	Name    *string `json:"name,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}

func NormalizeAccessPatch(patch AccessPatch) (AccessPatch, error) {
	if patch.Name == nil && patch.Enabled == nil {
		return AccessPatch{}, ValidationError{"patch", "at least one field is required"}
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if count := utf8.RuneCountInString(name); count < 1 || count > 100 || strings.ContainsFunc(name, unicode.IsControl) {
			return AccessPatch{}, ValidationError{"name", "expected 1 to 100 printable characters"}
		}
		patch.Name = &name
	}
	return patch, nil
}

type AccessInput struct {
	Name    string
	LineID  string
	Enabled bool
}

type Access struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	LineID      string    `json:"line_id"`
	Name        string    `json:"name"`
	Enabled     bool      `json:"enabled"`
	ApplyStatus string    `json:"apply_status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func NormalizeAccess(input NewAccess) (AccessInput, error) {
	name := strings.TrimSpace(input.Name)
	if count := utf8.RuneCountInString(name); count < 1 || count > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return AccessInput{}, ValidationError{"name", "expected 1 to 100 printable characters"}
	}
	if !uuidPattern.MatchString(input.LineID) {
		return AccessInput{}, ValidationError{"line_id", "expected UUID"}
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return AccessInput{Name: name, LineID: strings.ToLower(input.LineID), Enabled: enabled}, nil
}
