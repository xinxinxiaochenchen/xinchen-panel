package orchestration

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"controlplane/internal/agentruntime"
	"controlplane/internal/forward"
)

var errSecretStore = errors.New("relay secret store failure")
var relayLineIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var relayCertFingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type RelaySecretStore interface {
	GetOrCreate(context.Context, string, uint64, int) ([]byte, error)
}
type RelayHopFact struct {
	NodeID            string
	Role              agentruntime.RelayRole
	Host              string
	RelayPort         int
	Online            bool
	RelayCapable      bool
	ProxyCapable      bool
	AgentFingerprints []string
	RelayFingerprints []string
	AppliedRelay      bool
}
type RelayLineFacts struct {
	LineID     string
	Generation uint64
	Enabled    bool
	Hops       []RelayHopFact
}
type RelayDiagnostic struct {
	LineID string
	Reason string
}

// MemoryRelaySecretStore is useful for tests and local development. Production
// uses a database-backed implementation that encrypts values at rest.
type MemoryRelaySecretStore struct{ values map[string][]byte }

func (s *MemoryRelaySecretStore) GetOrCreate(_ context.Context, line string, generation uint64, edge int) ([]byte, error) {
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	key := relaySecretKey(line, generation, edge)
	if value, ok := s.values[key]; ok {
		return append([]byte(nil), value...), nil
	}
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("%w: generate relay secret: %v", errSecretStore, err)
	}
	s.values[key] = append([]byte(nil), value...)
	return value, nil
}
func relaySecretKey(line string, generation uint64, edge int) string {
	return fmt.Sprintf("%s/%d/%d", line, generation, edge)
}

type failingRelaySecretStore struct{}

func (failingRelaySecretStore) GetOrCreate(context.Context, string, uint64, int) ([]byte, error) {
	return nil, errSecretStore
}

func CompileRelaySnapshots(ctx context.Context, lines []RelayLineFacts, secrets RelaySecretStore) (map[string][]agentruntime.RelayConfig, []RelayDiagnostic, error) {
	if secrets == nil {
		return nil, nil, errors.New("relay secret store is required")
	}
	result := make(map[string][]agentruntime.RelayConfig)
	diagnostics := make([]RelayDiagnostic, 0)
	ordered := append([]RelayLineFacts(nil), lines...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].LineID < ordered[j].LineID })
	for _, line := range ordered {
		reason := validateRelayLineFacts(line)
		if reason != "" {
			diagnostics = append(diagnostics, RelayDiagnostic{line.LineID, reason})
			continue
		}
		edgeSecrets := make([][]byte, len(line.Hops)-1)
		for edge := range edgeSecrets {
			value, err := secrets.GetOrCreate(ctx, line.LineID, line.Generation, edge)
			if err != nil {
				return nil, nil, err
			}
			if len(value) != 32 || allZero(value) {
				return nil, nil, fmt.Errorf("%w: invalid secret for line %s edge %d", errSecretStore, line.LineID, edge)
			}
			edgeSecrets[edge] = append([]byte(nil), value...)
		}
		for index, hop := range line.Hops {
			if index == 0 && !downstreamRelayApplied(line) {
				diagnostics = append(diagnostics, RelayDiagnostic{line.LineID, "downstream relay generation is not applied"})
				continue
			}
			config := agentruntime.RelayConfig{LineID: line.LineID, Generation: line.Generation, Role: hop.Role}
			if index > 0 {
				config.PreviousNodeID = line.Hops[index-1].NodeID
				config.PreviousSecret = append([]byte(nil), edgeSecrets[index-1]...)
				config.PreviousFingerprints = append([]string(nil), line.Hops[index-1].AgentFingerprints...)
			}
			if index < len(line.Hops)-1 {
				next := line.Hops[index+1]
				config.Next = &agentruntime.RelayNextHop{NodeID: next.NodeID, Address: net.JoinHostPort(next.Host, strconv.Itoa(next.RelayPort)), Port: next.RelayPort, Secret: append([]byte(nil), edgeSecrets[index]...), Fingerprints: append([]string(nil), next.RelayFingerprints...)}
			}
			if err := agentruntime.ValidateRelayConfig([]agentruntime.RelayConfig{config}); err != nil {
				return nil, nil, fmt.Errorf("compile relay line %s: %w", line.LineID, err)
			}
			result[hop.NodeID] = append(result[hop.NodeID], config)
		}
	}
	for node, configs := range result {
		slices.SortFunc(configs, func(a, b agentruntime.RelayConfig) int { return strings.Compare(a.LineID, b.LineID) })
		result[node] = configs
	}
	return result, diagnostics, nil
}

func downstreamRelayApplied(line RelayLineFacts) bool {
	for _, hop := range line.Hops[1:] {
		if !hop.AppliedRelay {
			return false
		}
	}
	return true
}
func validateRelayLineFacts(line RelayLineFacts) string {
	if !relayLineIDPattern.MatchString(line.LineID) {
		return "invalid line ID"
	}
	if !line.Enabled {
		return "line disabled"
	}
	if line.Generation == 0 {
		return "invalid generation"
	}
	if len(line.Hops) < 2 || len(line.Hops) > 8 {
		return "hop count must be between 2 and 8"
	}
	seen := map[string]bool{}
	for i, hop := range line.Hops {
		if !relayLineIDPattern.MatchString(hop.NodeID) {
			return "invalid node ID"
		}
		if seen[hop.NodeID] {
			return "duplicate node"
		}
		seen[hop.NodeID] = true
		if hop.Host == "" || !forward.ValidPublicHost(hop.Host) {
			return "invalid relay host"
		}
		if hop.RelayPort < 1024 || hop.RelayPort > 65535 {
			return "invalid relay port"
		}
		expected := agentruntime.RelayMiddle
		if i == 0 {
			expected = agentruntime.RelayIngress
		}
		if i == len(line.Hops)-1 {
			expected = agentruntime.RelayEgress
		}
		if hop.Role != expected {
			return "invalid hop role order"
		}
		if !hop.Online {
			return "hop Agent is not online"
		}
		if !hop.RelayCapable {
			return "hop Agent lacks relay capability"
		}
		if !validRelayCertFingerprints(hop.AgentFingerprints) || !validRelayCertFingerprints(hop.RelayFingerprints) {
			return "hop certificate grants are unavailable"
		}
		if i == 0 && !hop.ProxyCapable {
			return "ingress Agent lacks proxy capability"
		}
	}
	return ""
}

func validRelayCertFingerprints(values []string) bool {
	if len(values) < 1 || len(values) > 3 {
		return false
	}
	for _, value := range values {
		if !relayCertFingerprintPattern.MatchString(value) {
			return false
		}
	}
	return true
}
func allZero(value []byte) bool {
	for _, b := range value {
		if b != 0 {
			return false
		}
	}
	return true
}
