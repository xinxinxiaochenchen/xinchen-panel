package agentruntime

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strings"

	"controlplane/internal/forward"
)

type RelayRole string

const (
	RelayIngress RelayRole = "ingress"
	RelayMiddle  RelayRole = "relay"
	RelayEgress  RelayRole = "egress"
)

type RelayNextHop struct {
	NodeID  string `json:"node_id"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Secret  []byte `json:"secret"`
}
type RelayConfig struct {
	LineID         string        `json:"line_id"`
	Generation     uint64        `json:"generation"`
	Role           RelayRole     `json:"role"`
	PreviousNodeID string        `json:"previous_node_id,omitempty"`
	PreviousSecret []byte        `json:"previous_secret,omitempty"`
	Next           *RelayNextHop `json:"next,omitempty"`
	TargetHost     string        `json:"target_host,omitempty"`
	TargetPort     int           `json:"target_port,omitempty"`
}

func ValidateRelayConfig(configs []RelayConfig) error {
	seen := map[string]struct{}{}
	for _, c := range configs {
		if strings.TrimSpace(c.LineID) == "" || c.LineID != strings.TrimSpace(c.LineID) {
			return errors.New("relay line ID is required")
		}
		if c.Generation == 0 {
			return fmt.Errorf("relay line %q has invalid generation", c.LineID)
		}
		if _, ok := seen[c.LineID]; ok {
			return fmt.Errorf("duplicate relay line %q", c.LineID)
		}
		seen[c.LineID] = struct{}{}
		if !validSecret(c.PreviousSecret) && len(c.PreviousSecret) != 0 {
			return fmt.Errorf("relay line %q has invalid previous secret", c.LineID)
		}
		switch c.Role {
		case RelayIngress:
			if c.PreviousNodeID != "" || len(c.PreviousSecret) != 0 || !validNext(c.Next) || c.TargetHost != "" || c.TargetPort != 0 {
				return fmt.Errorf("relay ingress %q is invalid", c.LineID)
			}
		case RelayMiddle:
			if c.PreviousNodeID == "" || !validSecret(c.PreviousSecret) || !validNext(c.Next) || c.TargetHost != "" || c.TargetPort != 0 {
				return fmt.Errorf("relay middle %q is invalid", c.LineID)
			}
		case RelayEgress:
			if c.PreviousNodeID == "" || !validSecret(c.PreviousSecret) || c.Next != nil || c.TargetPort < 1 || c.TargetPort > 65535 || c.TargetHost != strings.TrimSpace(c.TargetHost) || !forward.ValidPublicHost(c.TargetHost) {
				return fmt.Errorf("relay egress %q is invalid", c.LineID)
			}
		default:
			return fmt.Errorf("relay line %q has invalid role", c.LineID)
		}
	}
	return nil
}
func validNext(n *RelayNextHop) bool {
	if n == nil || strings.TrimSpace(n.NodeID) != n.NodeID || n.NodeID == "" || strings.TrimSpace(n.Address) != n.Address || n.Address == "" || n.Port < 1 || n.Port > 65535 || !validSecret(n.Secret) {
		return false
	}
	host, port, err := net.SplitHostPort(n.Address)
	return err == nil && host != "" && port != ""
}
func validSecret(v []byte) bool { return len(v) == 32 && !bytes.Equal(v, make([]byte, 32)) }
