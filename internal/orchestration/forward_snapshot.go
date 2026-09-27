package orchestration

import (
	"fmt"
	"slices"
	"sort"

	"controlplane/internal/agentruntime"
)

// NodeFacts describes the ingress node at the time a complete snapshot is read.
type NodeFacts struct {
	ID             string
	GroupID        string
	Enabled        bool
	GroupEnabled   bool
	ForwardCapable bool
	ProxyCapable   bool
	ProxyPort      int
}

// ForwardFacts contains current authorization and target facts for one rule.
type ForwardFacts struct {
	ID                     string
	IngressNodeID          string
	IngressPort            int
	TargetNodeID           string
	TargetGroupID          string
	TargetHost             string
	TargetPort             int
	Protocol               string
	Enabled                bool
	OwnerActive            bool
	MembershipActive       bool
	MemberGroupIDs         []string
	MaxForwardRulesPerNode int
	TargetNodeEnabled      bool
	TargetGroupEnabled     bool
	TCPPolicyAllowed       bool
	UDPPolicyAllowed       bool
	EligibilityError       string
	LineID                 string
	RelayGeneration        uint64
	LineEnabled            bool
	LineOwnerID            string
	OwnerID                string
	MemberLineIDs          []string
	AllowCustomLines       bool
	MaxHops                int
	HopGroupIDs            []string
}

type ForwardRejection struct {
	RuleID string
	Reason string
}

type CompiledForwardSnapshot struct {
	Snapshot agentruntime.Snapshot
	Rejected []ForwardRejection
}

// CompileForwardSnapshot omits malformed rules individually so they cannot
// prevent unrelated authorization revocations from reaching the Agent.
func CompileForwardSnapshot(node NodeFacts, facts []ForwardFacts, revision uint64, relays ...[]agentruntime.RelayConfig) (CompiledForwardSnapshot, error) {
	var relayConfig []agentruntime.RelayConfig
	if len(relays) > 0 {
		relayConfig = relays[0]
	}
	result := CompiledForwardSnapshot{Snapshot: agentruntime.Snapshot{Revision: revision, RelayConfig: relayConfig,
		Rules: make([]agentruntime.Rule, 0, len(facts))}}
	if _, err := agentruntime.ValidateSnapshot(result.Snapshot); err != nil {
		return CompiledForwardSnapshot{}, err
	}
	if !node.Enabled || !node.GroupEnabled || !node.ForwardCapable {
		return result, nil
	}
	ordered := append([]ForwardFacts(nil), facts...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	usedPorts := make(map[string]struct{})
	usedIDs := make(map[string]struct{})
	for _, fact := range ordered {
		if fact.IngressNodeID != node.ID {
			result.Rejected = append(result.Rejected, ForwardRejection{fact.ID, "rule belongs to another ingress node"})
			continue
		}
		if fact.EligibilityError != "" {
			result.Rejected = append(result.Rejected, ForwardRejection{fact.ID, fact.EligibilityError})
			continue
		}
		if !fact.Enabled || !fact.OwnerActive || !fact.MembershipActive || fact.MaxForwardRulesPerNode <= 0 ||
			!slices.Contains(fact.MemberGroupIDs, node.GroupID) {
			continue
		}
		if fact.TargetNodeID != "" && (!fact.TargetNodeEnabled || !fact.TargetGroupEnabled ||
			!slices.Contains(fact.MemberGroupIDs, fact.TargetGroupID)) {
			continue
		}
		if fact.LineID != "" {
			if !fact.LineEnabled || fact.RelayGeneration == 0 || len(fact.HopGroupIDs) < 2 ||
				len(fact.HopGroupIDs) > fact.MaxHops ||
				(fact.LineOwnerID == "" && !slices.Contains(fact.MemberLineIDs, fact.LineID) ||
					fact.LineOwnerID != "" && (fact.LineOwnerID != fact.OwnerID || !fact.AllowCustomLines)) {
				continue
			}
			granted := true
			for _, groupID := range fact.HopGroupIDs {
				if !slices.Contains(fact.MemberGroupIDs, groupID) {
					granted = false
					break
				}
			}
			if !granted {
				continue
			}
		}
		switch fact.Protocol {
		case "TCP":
			if !fact.TCPPolicyAllowed {
				continue
			}
		case "UDP":
			if !fact.UDPPolicyAllowed {
				continue
			}
		case "BOTH":
			if !fact.TCPPolicyAllowed || !fact.UDPPolicyAllowed {
				continue
			}
		}
		if node.ProxyCapable && fact.IngressPort == node.ProxyPort && (fact.Protocol == "TCP" || fact.Protocol == "BOTH") {
			result.Rejected = append(result.Rejected, ForwardRejection{RuleID: fact.ID, Reason: "port reserved for proxy TLS listener"})
			continue
		}
		rule := agentruntime.Rule{ID: fact.ID, IngressPort: fact.IngressPort,
			TargetHost: fact.TargetHost, TargetPort: fact.TargetPort, Protocol: fact.Protocol, Enabled: true,
			LineID: fact.LineID, RelayGeneration: fact.RelayGeneration}
		if _, err := agentruntime.ValidateSnapshot(agentruntime.Snapshot{Revision: revision, Rules: []agentruntime.Rule{rule}, RelayConfig: relayConfig}); err != nil {
			result.Rejected = append(result.Rejected, ForwardRejection{fact.ID, err.Error()})
			continue
		}
		if _, duplicate := usedIDs[fact.ID]; duplicate {
			result.Rejected = append(result.Rejected, ForwardRejection{fact.ID, "duplicate rule ID"})
			continue
		}
		protocols := []string{rule.Protocol}
		if rule.Protocol == "BOTH" {
			protocols = []string{"TCP", "UDP"}
		}
		conflict := false
		for _, protocol := range protocols {
			if _, taken := usedPorts[fmt.Sprintf("%s:%d", protocol, rule.IngressPort)]; taken {
				conflict = true
			}
		}
		if conflict {
			result.Rejected = append(result.Rejected, ForwardRejection{fact.ID, "duplicate physical listener"})
			continue
		}
		usedIDs[fact.ID] = struct{}{}
		for _, protocol := range protocols {
			usedPorts[fmt.Sprintf("%s:%d", protocol, rule.IngressPort)] = struct{}{}
		}
		result.Snapshot.Rules = append(result.Snapshot.Rules, rule)
	}
	if _, err := agentruntime.ValidateSnapshot(result.Snapshot); err != nil {
		result.Rejected = append(result.Rejected, ForwardRejection{"", "full snapshot validation: " + err.Error()})
		result.Snapshot.Rules = nil
	}
	return result, nil
}
