package agentruntime

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

type RelayRole string

const (
	RelayIngress RelayRole = "ingress"
	RelayMiddle  RelayRole = "relay"
	RelayEgress  RelayRole = "egress"
)

type RelayNextHop struct {
	NodeID       string   `json:"node_id"`
	Address      string   `json:"address"`
	Port         int      `json:"port"`
	Secret       []byte   `json:"secret"`
	Fingerprints []string `json:"fingerprints,omitempty"`
}
type RelayConfig struct {
	LineID               string        `json:"line_id"`
	Generation           uint64        `json:"generation"`
	Role                 RelayRole     `json:"role"`
	PreviousNodeID       string        `json:"previous_node_id,omitempty"`
	PreviousSecret       []byte        `json:"previous_secret,omitempty"`
	PreviousFingerprints []string      `json:"previous_fingerprints,omitempty"`
	Next                 *RelayNextHop `json:"next,omitempty"`
}

var relayFingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validFingerprints(values []string) bool {
	if len(values) < 1 || len(values) > 2 {
		return false
	}
	for _, value := range values {
		if !relayFingerprintPattern.MatchString(value) {
			return false
		}
	}
	return true
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
			if c.PreviousNodeID != "" || len(c.PreviousSecret) != 0 || len(c.PreviousFingerprints) != 0 || !validNext(c.Next) {
				return fmt.Errorf("relay ingress %q is invalid", c.LineID)
			}
		case RelayMiddle:
			if c.PreviousNodeID == "" || !validSecret(c.PreviousSecret) || !validFingerprints(c.PreviousFingerprints) || !validNext(c.Next) {
				return fmt.Errorf("relay middle %q is invalid", c.LineID)
			}
		case RelayEgress:
			if c.PreviousNodeID == "" || !validSecret(c.PreviousSecret) || !validFingerprints(c.PreviousFingerprints) || c.Next != nil {
				return fmt.Errorf("relay egress %q is invalid", c.LineID)
			}
		default:
			return fmt.Errorf("relay line %q has invalid role", c.LineID)
		}
	}
	return nil
}
func validNext(n *RelayNextHop) bool {
	if n == nil || strings.TrimSpace(n.NodeID) != n.NodeID || n.NodeID == "" || strings.TrimSpace(n.Address) != n.Address || n.Address == "" || n.Port < 1 || n.Port > 65535 || !validSecret(n.Secret) || !validFingerprints(n.Fingerprints) {
		return false
	}
	host, port, err := net.SplitHostPort(n.Address)
	return err == nil && host != "" && port != ""
}
func validSecret(v []byte) bool { return len(v) == 32 && !bytes.Equal(v, make([]byte, 32)) }
