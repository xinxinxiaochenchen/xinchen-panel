package entitlement

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type ValidationError struct {
	Field  string
	Reason string
}

func (e ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

type PlanLimits struct {
	MaxForwardRulesPerNode int  `json:"max_forward_rules_per_node"`
	MaxSubscriptions       int  `json:"max_subscriptions"`
	MaxRoutingRules        int  `json:"max_routing_rules"`
	AllowCustomLines       bool `json:"allow_custom_lines"`
	MaxCustomLines         int  `json:"max_custom_lines"`
	MaxHops                int  `json:"max_hops"`
}

type NewPlan struct {
	Name                   string     `json:"name"`
	QuotaBytes             int64      `json:"quota_bytes"`
	DefaultMultiplierMilli *int       `json:"default_multiplier_milli,omitempty"`
	Limits                 PlanLimits `json:"limits"`
	ResourceGroupIDs       []string   `json:"resource_group_ids"`
	LineIDs                []string   `json:"line_ids"`
}

type PlanInput struct {
	Name                   string
	QuotaBytes             int64
	DefaultMultiplierMilli int
	Limits                 PlanLimits
	ResourceGroupIDs       []string
	LineIDs                []string
}

type Plan struct {
	ID                     string     `json:"id"`
	Name                   string     `json:"name"`
	BillingMode            string     `json:"billing_mode"`
	PeriodMonths           int        `json:"period_months"`
	QuotaBytes             int64      `json:"quota_bytes"`
	DefaultMultiplierMilli int        `json:"default_multiplier_milli"`
	Status                 string     `json:"status"`
	Limits                 PlanLimits `json:"limits"`
	ResourceGroupIDs       []string   `json:"resource_group_ids"`
	LineIDs                []string   `json:"line_ids"`
	CreatedAt              time.Time  `json:"created_at"`
}

type Snapshot struct {
	PlanName               string     `json:"plan_name"`
	QuotaBytes             int64      `json:"quota_bytes"`
	DefaultMultiplierMilli int        `json:"default_multiplier_milli"`
	Limits                 PlanLimits `json:"limits"`
	ResourceGroupIDs       []string   `json:"resource_group_ids"`
	LineIDs                []string   `json:"line_ids"`
}

func (p Plan) Snapshot() Snapshot {
	return Snapshot{PlanName: p.Name, QuotaBytes: p.QuotaBytes,
		DefaultMultiplierMilli: p.DefaultMultiplierMilli, Limits: p.Limits,
		ResourceGroupIDs: append([]string{}, p.ResourceGroupIDs...),
		LineIDs:          append([]string{}, p.LineIDs...)}
}

func NormalizePlan(input NewPlan) (PlanInput, error) {
	name := strings.TrimSpace(input.Name)
	if len(name) == 0 || len(name) > 100 {
		return PlanInput{}, ValidationError{"name", "expected 1 to 100 characters"}
	}
	if input.QuotaBytes < 0 {
		return PlanInput{}, ValidationError{"quota_bytes", "must be non-negative"}
	}
	multiplier := 1000
	if input.DefaultMultiplierMilli != nil {
		multiplier = *input.DefaultMultiplierMilli
	}
	if multiplier < 1 || multiplier > 100000 {
		return PlanInput{}, ValidationError{"default_multiplier_milli", "expected 1 to 100000"}
	}
	limits := input.Limits
	if limits.MaxHops == 0 {
		limits.MaxHops = 1
	}
	if limits.MaxHops < 1 || limits.MaxHops > 8 {
		return PlanInput{}, ValidationError{"limits.max_hops", "expected 1 to 8"}
	}
	if limits.MaxForwardRulesPerNode < 0 || limits.MaxSubscriptions < 0 || limits.MaxRoutingRules < 0 || limits.MaxCustomLines < 0 {
		return PlanInput{}, ValidationError{"limits", "limits must be non-negative"}
	}
	if !limits.AllowCustomLines && limits.MaxCustomLines != 0 {
		return PlanInput{}, ValidationError{"limits.max_custom_lines", "requires allow_custom_lines"}
	}
	groups, err := normalizeIDs("resource_group_ids", input.ResourceGroupIDs)
	if err != nil {
		return PlanInput{}, err
	}
	lines, err := normalizeIDs("line_ids", input.LineIDs)
	if err != nil {
		return PlanInput{}, err
	}
	return PlanInput{Name: name, QuotaBytes: input.QuotaBytes, DefaultMultiplierMilli: multiplier,
		Limits: limits, ResourceGroupIDs: groups, LineIDs: lines}, nil
}

func normalizeIDs(field string, values []string) ([]string, error) {
	if len(values) > 1000 {
		return nil, ValidationError{field, "too many grants"}
	}
	result := make([]string, len(values))
	seen := make(map[string]bool, len(values))
	for i, value := range values {
		if !uuidPattern.MatchString(value) {
			return nil, ValidationError{field, "expected UUID values"}
		}
		value = strings.ToLower(value)
		if seen[value] {
			return nil, ValidationError{field, "duplicate grant ID"}
		}
		seen[value] = true
		result[i] = value
	}
	sort.Strings(result)
	return result, nil
}

type NewMembership struct {
	UserID    string    `json:"user_id"`
	PlanID    string    `json:"plan_id"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	AnchorDay int       `json:"anchor_day"`
	Timezone  string    `json:"timezone"`
}

type MembershipInput NewMembership

type Membership struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	PlanID    string    `json:"plan_id"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	Status    string    `json:"status"`
	AnchorDay int       `json:"anchor_day"`
	Timezone  string    `json:"timezone"`
	Snapshot  Snapshot  `json:"snapshot"`
	CreatedAt time.Time `json:"created_at"`
}

func NormalizeMembership(input NewMembership, now time.Time) (MembershipInput, error) {
	if !uuidPattern.MatchString(input.UserID) {
		return MembershipInput{}, ValidationError{"user_id", "expected UUID"}
	}
	if !uuidPattern.MatchString(input.PlanID) {
		return MembershipInput{}, ValidationError{"plan_id", "expected UUID"}
	}
	if input.StartsAt.IsZero() || input.StartsAt.After(now) {
		return MembershipInput{}, ValidationError{"starts_at", "must have started"}
	}
	if !input.EndsAt.After(now.Add(5*time.Minute)) || !input.EndsAt.After(input.StartsAt) {
		return MembershipInput{}, ValidationError{"ends_at", "must be more than five minutes after now and after start"}
	}
	if input.AnchorDay < 1 || input.AnchorDay > 31 {
		return MembershipInput{}, ValidationError{"anchor_day", "expected 1 to 31"}
	}
	if input.Timezone == "" || input.Timezone == "Local" || input.Timezone == "Etc/Localtime" {
		return MembershipInput{}, ValidationError{"timezone", "required"}
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return MembershipInput{}, ValidationError{"timezone", "expected IANA timezone"}
	}
	input.UserID = strings.ToLower(input.UserID)
	input.PlanID = strings.ToLower(input.PlanID)
	input.StartsAt = input.StartsAt.UTC()
	input.EndsAt = input.EndsAt.UTC()
	return MembershipInput(input), nil
}
