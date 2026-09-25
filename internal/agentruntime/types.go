package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"controlplane/internal/forward"
)

type Rule struct {
	ID          string `json:"id"`
	IngressPort int    `json:"ingress_port"`
	TargetHost  string `json:"target_host"`
	TargetPort  int    `json:"target_port"`
	Protocol    string `json:"protocol"`
	Enabled     bool   `json:"enabled"`
}

type Snapshot struct {
	Revision uint64 `json:"revision"`
	Rules    []Rule `json:"forward_config"`
}

type listenerKey struct {
	protocol string
	port     int
}

type target struct {
	host string
	port int
	id   string
}

type Resolver func(context.Context, string) ([]netip.Addr, error)

var ErrNonPublicTarget = errors.New("forward target has no public IP address")

func ValidateSnapshot(snapshot Snapshot) (map[listenerKey]target, error) {
	if snapshot.Revision == 0 {
		return nil, errors.New("snapshot revision must be positive")
	}
	listeners := make(map[listenerKey]target)
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
			listeners[key] = target{host: rule.TargetHost, port: rule.TargetPort, id: rule.ID}
		}
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
