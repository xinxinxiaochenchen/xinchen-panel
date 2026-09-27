package forward

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"controlplane/internal/catalog"
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
var dnsTLD = regexp.MustCompile(`^[a-z]{2,63}$`)

var nonPublicRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

type ValidationError struct {
	Field  string
	Reason string
}

func (e ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

type NewRule struct {
	Name          string  `json:"name"`
	IngressNodeID string  `json:"ingress_node_id"`
	IngressPort   int     `json:"ingress_port"`
	TargetNodeID  *string `json:"target_node_id,omitempty"`
	TargetHost    *string `json:"target_host,omitempty"`
	TargetPort    int     `json:"target_port"`
	Protocol      string  `json:"protocol"`
	Enabled       *bool   `json:"enabled,omitempty"`
	LineID        *string `json:"line_id,omitempty"`
}

type RuleInput struct {
	Name          string
	IngressNodeID string
	IngressPort   int
	TargetNodeID  *string
	TargetHost    *string
	TargetPort    int
	Protocol      string
	Enabled       bool
	LineID        *string
}

type RulePatch struct {
	Name    *string `json:"name,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}

type Rule struct {
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	Name          string    `json:"name"`
	IngressNodeID string    `json:"ingress_node_id"`
	IngressPort   int       `json:"ingress_port"`
	TargetNodeID  *string   `json:"target_node_id"`
	TargetHost    *string   `json:"target_host"`
	TargetPort    int       `json:"target_port"`
	LineID        *string   `json:"line_id"`
	Protocol      string    `json:"protocol"`
	Enabled       bool      `json:"enabled"`
	ApplyStatus   string    `json:"apply_status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func NormalizeRule(input NewRule) (RuleInput, error) {
	name := strings.TrimSpace(input.Name)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 {
		return RuleInput{}, ValidationError{"name", "expected 1 to 100 characters"}
	}
	if !catalog.ValidID(input.IngressNodeID) {
		return RuleInput{}, ValidationError{"ingress_node_id", "expected UUID"}
	}
	if input.IngressPort < 1024 || input.IngressPort > 65535 {
		return RuleInput{}, ValidationError{"ingress_port", "expected port 1024 to 65535"}
	}
	if (input.TargetNodeID == nil) == (input.TargetHost == nil) {
		return RuleInput{}, ValidationError{"target", "exactly one of target_node_id or target_host is required"}
	}
	var targetNodeID, targetHost *string
	if input.TargetNodeID != nil {
		if input.LineID != nil {
			return RuleInput{}, ValidationError{"line_id", "line-bound rules require a public host target"}
		}
		if !catalog.ValidID(*input.TargetNodeID) {
			return RuleInput{}, ValidationError{"target_node_id", "expected UUID"}
		}
		value := strings.ToLower(*input.TargetNodeID)
		targetNodeID = &value
	} else {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(*input.TargetHost), "."))
		if !ValidPublicHost(host) {
			return RuleInput{}, ValidationError{"target_host", "expected public IP address or DNS hostname"}
		}
		targetHost = &host
	}
	var lineID *string
	if input.LineID != nil {
		if !catalog.ValidID(*input.LineID) {
			return RuleInput{}, ValidationError{"line_id", "expected UUID"}
		}
		value := strings.ToLower(*input.LineID)
		lineID = &value
	}
	if input.TargetPort < 1 || input.TargetPort > 65535 {
		return RuleInput{}, ValidationError{"target_port", "expected port 1 to 65535"}
	}
	if input.Protocol != "TCP" && input.Protocol != "UDP" && input.Protocol != "BOTH" {
		return RuleInput{}, ValidationError{"protocol", "expected TCP, UDP or BOTH"}
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return RuleInput{Name: name, IngressNodeID: strings.ToLower(input.IngressNodeID),
		IngressPort: input.IngressPort, TargetNodeID: targetNodeID, TargetHost: targetHost,
		TargetPort: input.TargetPort, Protocol: input.Protocol, Enabled: enabled, LineID: lineID}, nil
}

func NormalizeRulePatch(input RulePatch) (RulePatch, error) {
	if input.Name == nil && input.Enabled == nil {
		return RulePatch{}, ValidationError{"body", "at least one field is required"}
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 {
			return RulePatch{}, ValidationError{"name", "expected 1 to 100 characters"}
		}
		input.Name = &name
	}
	return input, nil
}

// ValidPublicHost accepts a public IP literal or a well-formed DNS hostname.
// DNS results still require PublicIP validation at connection time.
func ValidPublicHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return PublicIP(addr)
	}
	if len(host) < 4 || len(host) > 253 || strings.Contains(host, ":") || strings.Contains(host, "..") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 || !dnsTLD.MatchString(labels[len(labels)-1]) {
		return false
	}
	for _, label := range labels {
		if len(label) > 63 || !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// PublicIP is also the destination policy the future Agent must apply to every resolved address.
func PublicIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.Zone() != "" || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}
	for _, prefix := range nonPublicRanges {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
