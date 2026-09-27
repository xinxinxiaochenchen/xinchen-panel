package catalog

import (
	"strings"
	"time"
)

type NewLine struct {
	Name            string    `json:"name"`
	NodeID          string    `json:"node_id"`
	Hops            []LineHop `json:"hops,omitempty"`
	Enabled         *bool     `json:"enabled,omitempty"`
	Priority        *int      `json:"priority,omitempty"`
	Weight          *int      `json:"weight,omitempty"`
	MultiplierMilli *int      `json:"multiplier_milli,omitempty"`
	Tags            []string  `json:"tags,omitempty"`
}

type LineInput struct {
	Name            string
	NodeID          string
	Hops            []LineHop
	Enabled         bool
	Priority        int
	Weight          int
	MultiplierMilli *int
	Tags            []string
}

type LinePatch struct {
	Name            *string   `json:"name,omitempty"`
	Enabled         *bool     `json:"enabled,omitempty"`
	Priority        *int      `json:"priority,omitempty"`
	Weight          *int      `json:"weight,omitempty"`
	MultiplierMilli *int      `json:"multiplier_milli,omitempty"`
	Tags            *[]string `json:"tags,omitempty"`
}

type LineHop struct {
	Position int    `json:"position"`
	NodeID   string `json:"node_id"`
	Role     string `json:"role"`
}

type Line struct {
	ID              string    `json:"id"`
	OwnerUserID     *string   `json:"owner_user_id"`
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	Priority        int       `json:"priority"`
	Weight          int       `json:"weight"`
	MultiplierMilli *int      `json:"multiplier_milli"`
	Tags            []string  `json:"tags"`
	Hops            []LineHop `json:"hops"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func NormalizeLine(input NewLine, shared bool) (LineInput, error) {
	name := strings.TrimSpace(input.Name)
	if len(name) < 1 || len(name) > 100 {
		return LineInput{}, ValidationError{"name", "expected 1 to 100 characters"}
	}
	if input.Hops != nil {
		if input.NodeID != "" {
			return LineInput{}, ValidationError{"hops", "cannot combine hops and node_id"}
		}
		if err := validateLineTopology(input.Hops); err != nil {
			return LineInput{}, err
		}
		if len(input.Hops) < 2 {
			return LineInput{}, ValidationError{"hops", "use node_id for single-hop lines"}
		}
	} else if !ValidID(input.NodeID) {
		return LineInput{}, ValidationError{"node_id", "expected UUID"}
	}
	priority, weight, enabled := 100, 1, true
	if input.Priority != nil {
		priority = *input.Priority
	}
	if priority < 0 || priority > 1000 {
		return LineInput{}, ValidationError{"priority", "expected 0 to 1000"}
	}
	if input.Weight != nil {
		weight = *input.Weight
	}
	if weight < 1 || weight > 100 {
		return LineInput{}, ValidationError{"weight", "expected 1 to 100"}
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.MultiplierMilli != nil {
		if !shared {
			return LineInput{}, ValidationError{"multiplier_milli", "member lines cannot override billing multiplier"}
		}
		if *input.MultiplierMilli < 1 || *input.MultiplierMilli > 100000 {
			return LineInput{}, ValidationError{"multiplier_milli", "expected 1 to 100000"}
		}
	}
	if len(input.Tags) > 16 {
		return LineInput{}, ValidationError{"tags", "too many tags"}
	}
	tags := make([]string, 0, len(input.Tags))
	seen := make(map[string]bool, len(input.Tags))
	for _, tag := range input.Tags {
		tag = strings.TrimSpace(tag)
		if len(tag) < 1 || len(tag) > 32 || seen[tag] {
			return LineInput{}, ValidationError{"tags", "expected unique tags of 1 to 32 characters"}
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	hops := append([]LineHop(nil), input.Hops...)
	for i := range hops {
		hops[i].NodeID = strings.ToLower(hops[i].NodeID)
	}
	return LineInput{Name: name, NodeID: strings.ToLower(input.NodeID), Hops: hops, Enabled: enabled, Priority: priority,
		Weight: weight, MultiplierMilli: input.MultiplierMilli, Tags: tags}, nil
}

func validateLineTopology(hops []LineHop) error {
	if len(hops) < 1 || len(hops) > 8 {
		return ValidationError{"hops", "expected 1 to 8 ordered hops"}
	}
	seen := make(map[string]bool, len(hops))
	for position, hop := range hops {
		nodeID := strings.ToLower(hop.NodeID)
		role := "relay"
		if position == 0 {
			role = "ingress"
		}
		if position == len(hops)-1 {
			role = "egress"
		}
		if hop.Position != position || hop.Role != role || !ValidID(nodeID) || seen[nodeID] {
			return ValidationError{"hops", "expected unique node UUIDs, contiguous positions and ingress/relay/egress order"}
		}
		seen[nodeID] = true
	}
	return nil
}

func NormalizeLinePatch(input LinePatch, shared bool) (LinePatch, error) {
	if input.Name == nil && input.Enabled == nil && input.Priority == nil && input.Weight == nil && input.MultiplierMilli == nil && input.Tags == nil {
		return LinePatch{}, ValidationError{"body", "expected at least one change"}
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if len(name) < 1 || len(name) > 100 {
			return LinePatch{}, ValidationError{"name", "expected 1 to 100 characters"}
		}
		input.Name = &name
	}
	if input.Priority != nil && (*input.Priority < 0 || *input.Priority > 1000) {
		return LinePatch{}, ValidationError{"priority", "expected 0 to 1000"}
	}
	if input.Weight != nil && (*input.Weight < 1 || *input.Weight > 100) {
		return LinePatch{}, ValidationError{"weight", "expected 1 to 100"}
	}
	if input.MultiplierMilli != nil {
		if !shared {
			return LinePatch{}, ValidationError{"multiplier_milli", "member lines cannot override billing multiplier"}
		}
		if *input.MultiplierMilli < 1 || *input.MultiplierMilli > 100000 {
			return LinePatch{}, ValidationError{"multiplier_milli", "expected 1 to 100000"}
		}
	}
	if input.Tags != nil {
		if len(*input.Tags) > 16 {
			return LinePatch{}, ValidationError{"tags", "too many tags"}
		}
		tags := make([]string, 0, len(*input.Tags))
		seen := make(map[string]bool, len(*input.Tags))
		for _, tag := range *input.Tags {
			tag = strings.TrimSpace(tag)
			if len(tag) < 1 || len(tag) > 32 || seen[tag] {
				return LinePatch{}, ValidationError{"tags", "expected unique tags of 1 to 32 characters"}
			}
			seen[tag] = true
			tags = append(tags, tag)
		}
		input.Tags = &tags
	}
	return input, nil
}
