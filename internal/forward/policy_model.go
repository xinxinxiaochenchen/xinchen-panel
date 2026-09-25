package forward

import (
	"strings"
	"time"

	"controlplane/internal/catalog"
)

type NewTargetPolicy struct {
	Kind          string  `json:"kind"`
	TargetGroupID *string `json:"target_group_id,omitempty"`
	Protocol      string  `json:"protocol"`
	PortStart     int     `json:"port_start"`
	PortEnd       int     `json:"port_end"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

type TargetPolicyInput struct {
	Kind          string
	TargetGroupID *string
	Protocol      string
	PortStart     int
	PortEnd       int
	Enabled       bool
}

type TargetPolicyPatch struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type TargetPolicy struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	TargetGroupID *string   `json:"target_group_id"`
	Protocol      string    `json:"protocol"`
	PortStart     int       `json:"port_start"`
	PortEnd       int       `json:"port_end"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func NormalizeTargetPolicy(input NewTargetPolicy) (TargetPolicyInput, error) {
	if input.Kind != "public_host" && input.Kind != "node" {
		return TargetPolicyInput{}, ValidationError{"kind", "expected public_host or node"}
	}
	var groupID *string
	if input.Kind == "public_host" {
		if input.TargetGroupID != nil {
			return TargetPolicyInput{}, ValidationError{"target_group_id", "not permitted for public_host"}
		}
	} else {
		if input.TargetGroupID == nil || !catalog.ValidID(*input.TargetGroupID) {
			return TargetPolicyInput{}, ValidationError{"target_group_id", "expected resource group UUID"}
		}
		value := strings.ToLower(*input.TargetGroupID)
		groupID = &value
	}
	if input.Protocol != "TCP" && input.Protocol != "UDP" {
		return TargetPolicyInput{}, ValidationError{"protocol", "expected TCP or UDP"}
	}
	if input.PortStart < 1 || input.PortStart > 65535 {
		return TargetPolicyInput{}, ValidationError{"port_start", "expected port 1 to 65535"}
	}
	if input.PortEnd < input.PortStart || input.PortEnd > 65535 {
		return TargetPolicyInput{}, ValidationError{"port_end", "expected end port at least start port and at most 65535"}
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return TargetPolicyInput{Kind: input.Kind, TargetGroupID: groupID,
		Protocol: input.Protocol, PortStart: input.PortStart, PortEnd: input.PortEnd, Enabled: enabled}, nil
}

func NormalizeTargetPolicyPatch(input TargetPolicyPatch) (TargetPolicyPatch, error) {
	if input.Enabled == nil {
		return TargetPolicyPatch{}, ValidationError{"body", "enabled is required"}
	}
	return input, nil
}
