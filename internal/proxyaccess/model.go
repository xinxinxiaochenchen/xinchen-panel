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
	Name        string       `json:"name"`
	LineID      string       `json:"line_id,omitempty"`
	LineIDs     []string     `json:"line_ids,omitempty"`
	LineOptions []LineOption `json:"line_options,omitempty"`
	Enabled     *bool        `json:"enabled,omitempty"`
}

type LineOption struct {
	LineID   string `json:"line_id"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
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
	Name        string
	LineID      string
	LineIDs     []string
	LineOptions []LineOption
	Enabled     bool
}

type Access struct {
	ID          string       `json:"id"`
	UserID      string       `json:"user_id"`
	LineID      string       `json:"line_id"`
	LineIDs     []string     `json:"line_ids,omitempty"`
	LineOptions []LineOption `json:"line_options,omitempty"`
	Name        string       `json:"name"`
	Enabled     bool         `json:"enabled"`
	ApplyStatus string       `json:"apply_status"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

func NormalizeAccess(input NewAccess) (AccessInput, error) {
	name := strings.TrimSpace(input.Name)
	if count := utf8.RuneCountInString(name); count < 1 || count > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return AccessInput{}, ValidationError{"name", "expected 1 to 100 printable characters"}
	}
	lines := append([]string(nil), input.LineIDs...)
	if len(lines) == 0 && input.LineID != "" {
		lines = []string{input.LineID}
	}
	if len(lines) == 0 || len(lines) > 32 {
		return AccessInput{}, ValidationError{"line_ids", "expected 1 to 32 lines"}
	}
	if input.LineID != "" && !strings.EqualFold(input.LineID, lines[0]) {
		return AccessInput{}, ValidationError{"line_id", "must match first line_ids entry"}
	}
	seen := make(map[string]struct{}, len(lines))
	for index, line := range lines {
		if !uuidPattern.MatchString(line) {
			return AccessInput{}, ValidationError{"line_ids", "expected UUID"}
		}
		line = strings.ToLower(line)
		if _, found := seen[line]; found {
			return AccessInput{}, ValidationError{"line_ids", "duplicate line"}
		}
		seen[line] = struct{}{}
		lines[index] = line
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	options, err := normalizeLineOptions(lines, input.LineOptions)
	if err != nil {
		return AccessInput{}, err
	}
	return AccessInput{Name: name, LineID: lines[0], LineIDs: lines, LineOptions: options, Enabled: enabled}, nil
}

func normalizeLineOptions(lines []string, input []LineOption) ([]LineOption, error) {
	if len(input) == 0 {
		options := make([]LineOption, len(lines))
		for index, lineID := range lines {
			options[index] = LineOption{LineID: lineID, Priority: 100, Weight: 1}
		}
		return options, nil
	}
	if len(input) != len(lines) {
		return nil, ValidationError{"line_options", "must contain one option per line"}
	}
	allowed := make(map[string]struct{}, len(lines))
	for _, lineID := range lines {
		allowed[lineID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(input))
	byLine := make(map[string]LineOption, len(input))
	for _, option := range input {
		lineID := strings.ToLower(option.LineID)
		if _, ok := allowed[lineID]; !ok {
			return nil, ValidationError{"line_options", "line is not in line_ids"}
		}
		if _, ok := seen[lineID]; ok {
			return nil, ValidationError{"line_options", "duplicate line"}
		}
		if option.Priority < 0 || option.Priority > 1000 || option.Weight < 1 || option.Weight > 100 {
			return nil, ValidationError{"line_options", "priority or weight is out of range"}
		}
		seen[lineID] = struct{}{}
		byLine[lineID] = LineOption{LineID: lineID, Priority: option.Priority, Weight: option.Weight}
	}
	options := make([]LineOption, len(lines))
	for index, lineID := range lines {
		option, found := byLine[lineID]
		if !found {
			return nil, ValidationError{"line_options", "missing line option"}
		}
		options[index] = option
	}
	return options, nil
}
