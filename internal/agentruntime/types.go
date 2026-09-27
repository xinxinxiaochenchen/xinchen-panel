package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"controlplane/internal/forward"
)

type Rule struct {
	ID              string `json:"id"`
	IngressPort     int    `json:"ingress_port"`
	TargetHost      string `json:"target_host"`
	TargetPort      int    `json:"target_port"`
	Protocol        string `json:"protocol"`
	Enabled         bool   `json:"enabled"`
	LineID          string `json:"line_id,omitempty"`
	RelayGeneration uint64 `json:"relay_generation,omitempty"`
}

type Snapshot struct {
	Revision    uint64        `json:"revision"`
	Rules       []Rule        `json:"forward_config"`
	ProxyConfig []ProxyAccess `json:"proxy_config,omitempty"`
	RelayConfig []RelayConfig `json:"relay_config,omitempty"`
}

type ProxyAccess struct {
	ID              string               `json:"id"`
	UserID          string               `json:"user_id"`
	LineID          string               `json:"line_id"`
	Candidates      []ProxyLineCandidate `json:"candidates,omitempty"`
	RelayGeneration uint64               `json:"relay_generation,omitempty"`
	IngressPort     int                  `json:"ingress_port"`
	CredentialHash  string               `json:"credential_hash"`
	ExpiresAt       time.Time            `json:"expires_at"`
}

var trojanHashPattern = regexp.MustCompile(`^[0-9a-f]{56}$`)

type listenerKey struct {
	protocol string
	port     int
}

type target struct {
	host       string
	port       int
	id         string
	revision   uint64
	lineID     string
	generation uint64
}

type Resolver func(context.Context, string) ([]netip.Addr, error)

var ErrNonPublicTarget = errors.New("forward target has no public IP address")

func ValidateSnapshot(snapshot Snapshot) (map[listenerKey]target, error) {
	if snapshot.Revision == 0 {
		return nil, errors.New("snapshot revision must be positive")
	}
	if err := ValidateRelayConfig(snapshot.RelayConfig); err != nil {
		return nil, err
	}
	listeners := make(map[listenerKey]target)
	ingressRelays := make(map[string]uint64)
	for _, relay := range snapshot.RelayConfig {
		if relay.Role == RelayIngress {
			ingressRelays[relay.LineID] = relay.Generation
		}
	}
	ids := make(map[string]struct{}, len(snapshot.Rules))
	for _, rule := range snapshot.Rules {
		if rule.ID == "" || strings.TrimSpace(rule.ID) != rule.ID {
			return nil, errors.New("forward rule ID is required")
		}
		if _, ok := ids[rule.ID]; ok {
			return nil, fmt.Errorf("duplicate forward rule ID %q", rule.ID)
		}
		ids[rule.ID] = struct{}{}
		if !rule.Enabled {
			continue
		}
		if rule.IngressPort < 1024 || rule.IngressPort > 65535 || rule.TargetPort < 1 || rule.TargetPort > 65535 {
			return nil, fmt.Errorf("forward rule %q has invalid port", rule.ID)
		}
		if rule.TargetHost != strings.TrimSpace(rule.TargetHost) || !forward.ValidPublicHost(rule.TargetHost) {
			return nil, fmt.Errorf("forward rule %q has invalid target host", rule.ID)
		}
		if rule.LineID == "" && rule.RelayGeneration != 0 || rule.LineID != "" && (rule.RelayGeneration == 0 || ingressRelays[rule.LineID] != rule.RelayGeneration) {
			return nil, fmt.Errorf("forward rule %q requires a matching ingress relay", rule.ID)
		}
		var protocols []string
		switch rule.Protocol {
		case "TCP", "UDP":
			protocols = []string{rule.Protocol}
		case "BOTH":
			protocols = []string{"TCP", "UDP"}
		default:
			return nil, fmt.Errorf("forward rule %q has invalid protocol", rule.ID)
		}
		for _, protocol := range protocols {
			key := listenerKey{protocol: protocol, port: rule.IngressPort}
			if _, ok := listeners[key]; ok {
				return nil, fmt.Errorf("duplicate %s ingress port %d", protocol, rule.IngressPort)
			}
			listeners[key] = target{host: rule.TargetHost, port: rule.TargetPort, id: rule.ID, lineID: rule.LineID, generation: rule.RelayGeneration}
		}
	}
	proxyIDs := make(map[string]struct{}, len(snapshot.ProxyConfig))
	proxyHashes := make(map[string]struct{}, len(snapshot.ProxyConfig))
	for _, access := range snapshot.ProxyConfig {
		if access.ID == "" || access.UserID == "" || access.LineID == "" ||
			access.ID != strings.TrimSpace(access.ID) || access.IngressPort < 1 || access.IngressPort > 65535 ||
			!trojanHashPattern.MatchString(access.CredentialHash) || access.ExpiresAt.IsZero() {
			return nil, errors.New("invalid proxy access configuration")
		}
		if _, found := proxyIDs[access.ID]; found {
			return nil, errors.New("duplicate proxy access ID")
		}
		if _, found := proxyHashes[access.CredentialHash]; found {
			return nil, errors.New("duplicate proxy credential hash")
		}
		candidates := access.Candidates
		if len(candidates) == 0 {
			candidates = []ProxyLineCandidate{{LineID: access.LineID, RelayGeneration: access.RelayGeneration, Weight: 1}}
		}
		if len(candidates) > 32 {
			return nil, errors.New("proxy access has too many line candidates")
		}
		candidateIDs := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			if candidate.LineID == "" || candidate.Priority < 0 || candidate.Priority > 1000 || candidate.Weight < 1 || candidate.Weight > 100 {
				return nil, errors.New("invalid proxy line candidate")
			}
			if _, found := candidateIDs[candidate.LineID]; found {
				return nil, errors.New("duplicate proxy line candidate")
			}
			candidateIDs[candidate.LineID] = struct{}{}
			if candidate.LineID == access.LineID {
				if candidate.RelayGeneration != access.RelayGeneration {
					return nil, errors.New("proxy primary line generation mismatch")
				}
			}
			if expected, configured := ingressRelays[candidate.LineID]; configured {
				if candidate.RelayGeneration != expected {
					return nil, errors.New("proxy line candidate requires a matching ingress relay")
				}
			} else if candidate.RelayGeneration != 0 {
				return nil, errors.New("proxy line candidate has an unknown relay generation")
			}
		}
		if _, found := listeners[listenerKey{protocol: "TCP", port: access.IngressPort}]; found {
			return nil, errors.New("proxy listener conflicts with forward TCP listener")
		}
		if _, selected := candidateIDs[access.LineID]; selected && access.RelayGeneration != 0 && ingressRelays[access.LineID] != access.RelayGeneration {
			return nil, errors.New("proxy access requires a matching ingress relay")
		}
		proxyIDs[access.ID] = struct{}{}
		proxyHashes[access.CredentialHash] = struct{}{}
	}
	return listeners, nil
}

func ResolvePublic(ctx context.Context, host string, resolve Resolver) (netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		if forward.PublicIP(literal) {
			return literal.Unmap(), nil
		}
		return netip.Addr{}, ErrNonPublicTarget
	}
	if !forward.ValidPublicHost(host) || resolve == nil {
		return netip.Addr{}, ErrNonPublicTarget
	}
	addresses, err := resolve(ctx, host)
	if err != nil {
		return netip.Addr{}, err
	}
	for _, address := range addresses {
		if forward.PublicIP(address) {
			return address.Unmap(), nil
		}
	}
	return netip.Addr{}, ErrNonPublicTarget
}
