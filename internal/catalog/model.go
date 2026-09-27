package catalog

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

var (
	groupCodePattern = regexp.MustCompile(`^[A-Z0-9]+(?:\.[A-Z0-9]+)*$`)
	uuidPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hostLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
)

type ValidationError struct {
	Field  string
	Reason string
}

func (e ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

type NewGroup struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Region  string `json:"region"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type GroupInput struct {
	Code    string
	Name    string
	Region  string
	Enabled bool
}

type ResourceGroup struct {
	ID        string    `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Region    string    `json:"region"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

func NormalizeGroup(input NewGroup) (GroupInput, error) {
	if len(input.Code) == 0 || len(input.Code) > 64 || !groupCodePattern.MatchString(input.Code) {
		return GroupInput{}, ValidationError{"code", "expected uppercase dotted resource code"}
	}
	name := strings.TrimSpace(input.Name)
	region := strings.TrimSpace(input.Region)
	if len(name) == 0 || len(name) > 100 {
		return GroupInput{}, ValidationError{"name", "expected 1 to 100 characters"}
	}
	if len(region) == 0 || len(region) > 80 {
		return GroupInput{}, ValidationError{"region", "expected 1 to 80 characters"}
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return GroupInput{Code: input.Code, Name: name, Region: region, Enabled: enabled}, nil
}

type NewNode struct {
	GroupID         string   `json:"group_id"`
	Name            string   `json:"name"`
	Region          string   `json:"region"`
	Host            string   `json:"host"`
	PublicIP        *string  `json:"public_ip,omitempty"`
	ProxyPort       *int     `json:"proxy_port,omitempty"`
	RelayPort       *int     `json:"relay_port,omitempty"`
	Capabilities    []string `json:"capabilities"`
	BandwidthBPS    *int64   `json:"bandwidth_bps,omitempty"`
	MultiplierMilli *int     `json:"multiplier_milli,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
}

type NodeInput struct {
	GroupID         string
	Name            string
	Region          string
	Host            string
	PublicIP        *string
	ProxyPort       *int
	RelayPort       *int
	Capabilities    []string
	BandwidthBPS    *int64
	MultiplierMilli int
	Tags            []string
	Enabled         bool
}

type Node struct {
	ID              string     `json:"id"`
	GroupID         string     `json:"group_id"`
	GroupCode       string     `json:"group_code"`
	Name            string     `json:"name"`
	Region          string     `json:"region"`
	Host            string     `json:"host"`
	PublicIP        *string    `json:"public_ip"`
	ProxyPort       *int       `json:"proxy_port"`
	RelayPort       *int       `json:"relay_port"`
	Capabilities    []string   `json:"capabilities"`
	BandwidthBPS    *int64     `json:"bandwidth_bps"`
	MultiplierMilli int        `json:"multiplier_milli"`
	Tags            []string   `json:"tags"`
	Enabled         bool       `json:"enabled"`
	AgentStatus     string     `json:"agent_status"`
	LastSeenAt      *time.Time `json:"last_seen_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

func NormalizeNode(input NewNode) (NodeInput, error) {
	if !uuidPattern.MatchString(input.GroupID) {
		return NodeInput{}, ValidationError{"group_id", "expected UUID"}
	}
	name, region := strings.TrimSpace(input.Name), strings.TrimSpace(input.Region)
	if len(name) == 0 || len(name) > 100 {
		return NodeInput{}, ValidationError{"name", "expected 1 to 100 characters"}
	}
	if len(region) == 0 || len(region) > 80 {
		return NodeInput{}, ValidationError{"region", "expected 1 to 80 characters"}
	}
	host := strings.ToLower(strings.TrimSpace(input.Host))
	if !validHost(host) {
		return NodeInput{}, ValidationError{"host", "expected IP address or DNS hostname"}
	}
	if len(input.Capabilities) == 0 || len(input.Capabilities) > 2 {
		return NodeInput{}, ValidationError{"capabilities", "expected proxy and/or forward"}
	}
	capabilities := make([]string, 0, len(input.Capabilities))
	seen := map[string]bool{}
	for _, capability := range input.Capabilities {
		if (capability != "proxy" && capability != "forward") || seen[capability] {
			return NodeInput{}, ValidationError{"capabilities", "expected unique proxy/forward values"}
		}
		seen[capability] = true
		capabilities = append(capabilities, capability)
	}
	if seen["proxy"] && input.ProxyPort == nil {
		return NodeInput{}, ValidationError{"proxy_port", "required for proxy capability"}
	}
	if !seen["proxy"] && input.ProxyPort != nil {
		return NodeInput{}, ValidationError{"proxy_port", "requires proxy capability"}
	}
	if input.ProxyPort != nil && (*input.ProxyPort < 1 || *input.ProxyPort > 65535) {
		return NodeInput{}, ValidationError{"proxy_port", "expected port 1 to 65535"}
	}
	if input.RelayPort != nil {
		if !seen["forward"] || *input.RelayPort < 1024 || *input.RelayPort > 65535 ||
			(input.ProxyPort != nil && *input.RelayPort == *input.ProxyPort) {
			return NodeInput{}, ValidationError{"relay_port", "requires forward capability and distinct port 1024 to 65535"}
		}
	}
	var publicIP *string
	if input.PublicIP != nil {
		parsed, err := netip.ParseAddr(*input.PublicIP)
		if err != nil || parsed.Zone() != "" {
			return NodeInput{}, ValidationError{"public_ip", "expected IP address"}
		}
		value := parsed.String()
		publicIP = &value
	}
	if input.BandwidthBPS != nil && *input.BandwidthBPS < 0 {
		return NodeInput{}, ValidationError{"bandwidth_bps", "must be non-negative"}
	}
	multiplier := 1000
	if input.MultiplierMilli != nil {
		multiplier = *input.MultiplierMilli
	}
	if multiplier < 1 || multiplier > 100000 {
		return NodeInput{}, ValidationError{"multiplier_milli", "expected 1 to 100000"}
	}
	if len(input.Tags) > 16 {
		return NodeInput{}, ValidationError{"tags", "too many tags"}
	}
	tags := make([]string, 0, len(input.Tags))
	seenTags := map[string]bool{}
	for _, tag := range input.Tags {
		tag = strings.TrimSpace(tag)
		if len(tag) == 0 || len(tag) > 32 || seenTags[tag] {
			return NodeInput{}, ValidationError{"tags", "expected unique tags of 1 to 32 characters"}
		}
		seenTags[tag] = true
		tags = append(tags, tag)
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return NodeInput{GroupID: input.GroupID, Name: name, Region: region,
		Host: host, PublicIP: publicIP, ProxyPort: input.ProxyPort, RelayPort: input.RelayPort,
		Capabilities: capabilities, BandwidthBPS: input.BandwidthBPS,
		MultiplierMilli: multiplier, Tags: tags, Enabled: enabled}, nil
}

func validHost(value string) bool {
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.Zone() == ""
	}
	if len(value) < 1 || len(value) > 253 || strings.Contains(value, "..") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) > 63 || !hostLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func ValidID(value string) bool { return uuidPattern.MatchString(value) }

func ValidGroupCode(value string) bool {
	return len(value) <= 64 && groupCodePattern.MatchString(value)
}
