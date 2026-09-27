package orchestration

import (
	"slices"
	"sort"
	"time"

	"controlplane/internal/agentruntime"
)

type ProxyFacts struct {
	ID               string
	OwnerID          string
	LineID           string
	NodeID           string
	GroupID          string
	CredentialHash   string
	Enabled          bool
	OwnerActive      bool
	MembershipActive bool
	MemberGroupIDs   []string
	MemberLineIDs    []string
	AllowCustomLines bool
	LineOwnerID      string
	LineEnabled      bool
	RelayGeneration  uint64
	RelayReady       bool
	HopCount         int
	MaxHops          int
	HopGroupIDs      []string
	ExpiresAt        time.Time
	EligibilityError string
	Candidates       []ProxyCandidateFacts
}

type ProxyCandidateFacts struct {
	LineID          string
	NodeID          string
	GroupID         string
	LineOwnerID     string
	LineEnabled     bool
	RelayGeneration uint64
	RelayReady      bool
	HopCount        int
	MaxHops         int
	HopGroupIDs     []string
	Priority        int
	Weight          int
}

type CompiledProxySnapshot struct {
	Snapshot agentruntime.Snapshot
	Rejected []ForwardRejection
}

func CompileProxySnapshot(node NodeFacts, facts []ProxyFacts, relays []agentruntime.RelayConfig, revision uint64) (CompiledProxySnapshot, error) {
	result := CompiledProxySnapshot{Snapshot: agentruntime.Snapshot{Revision: revision, ProxyConfig: make([]agentruntime.ProxyAccess, 0)}}
	if !node.Enabled || !node.GroupEnabled || !node.ProxyCapable || node.ProxyPort < 1 || node.ProxyPort > 65535 {
		return result, nil
	}
	ordered := append([]ProxyFacts(nil), facts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	relayByLine := make(map[string]agentruntime.RelayConfig, len(relays))
	for _, relay := range relays {
		relayByLine[relay.LineID] = relay
	}
	for _, fact := range ordered {
		if fact.NodeID != node.ID || fact.GroupID != node.GroupID {
			result.Rejected = append(result.Rejected, ForwardRejection{RuleID: fact.ID, Reason: "proxy belongs to another node or resource group"})
			continue
		}
		if fact.EligibilityError != "" {
			result.Rejected = append(result.Rejected, ForwardRejection{RuleID: fact.ID, Reason: fact.EligibilityError})
			continue
		}
		if !fact.Enabled || !fact.OwnerActive || !fact.MembershipActive ||
			!slices.Contains(fact.MemberGroupIDs, node.GroupID) || !fact.ExpiresAt.After(time.Now()) {
			continue
		}
		candidateFacts := fact.Candidates
		if len(candidateFacts) == 0 && fact.LineEnabled {
			candidateFacts = []ProxyCandidateFacts{{LineID: fact.LineID, NodeID: fact.NodeID, GroupID: fact.GroupID,
				LineOwnerID: fact.LineOwnerID, LineEnabled: fact.LineEnabled, RelayGeneration: fact.RelayGeneration,
				RelayReady: fact.RelayReady, HopCount: fact.HopCount, MaxHops: fact.MaxHops, HopGroupIDs: fact.HopGroupIDs, Weight: 1}}
		}
		candidates := make([]agentruntime.ProxyLineCandidate, 0, len(candidateFacts))
		for _, candidate := range candidateFacts {
			if !proxyCandidateAllowed(node, fact, candidate, relayByLine) {
				continue
			}
			candidates = append(candidates, agentruntime.ProxyLineCandidate{LineID: candidate.LineID, RelayGeneration: candidate.RelayGeneration, Priority: candidate.Priority, Weight: candidate.Weight})
		}
		if len(candidates) == 0 {
			result.Rejected = append(result.Rejected, ForwardRejection{RuleID: fact.ID, Reason: "proxy has no authorized line candidates"})
			continue
		}
		orderedCandidates, err := agentruntime.RankProxyLineCandidates(fact.ID, candidates)
		if err != nil {
			result.Rejected = append(result.Rejected, ForwardRejection{RuleID: fact.ID, Reason: err.Error()})
			continue
		}
		primaryGeneration := uint64(0)
		for _, candidate := range orderedCandidates {
			if candidate.LineID == fact.LineID {
				primaryGeneration = candidate.RelayGeneration
				break
			}
		}
		access := agentruntime.ProxyAccess{ID: fact.ID, UserID: fact.OwnerID, LineID: fact.LineID,
			Candidates:      orderedCandidates,
			RelayGeneration: primaryGeneration, IngressPort: node.ProxyPort, CredentialHash: fact.CredentialHash, ExpiresAt: fact.ExpiresAt}
		trial := append(append([]agentruntime.ProxyAccess(nil), result.Snapshot.ProxyConfig...), access)
		if _, err := agentruntime.ValidateSnapshot(agentruntime.Snapshot{Revision: revision, ProxyConfig: trial, RelayConfig: relays}); err != nil {
			result.Rejected = append(result.Rejected, ForwardRejection{RuleID: fact.ID, Reason: err.Error()})
			continue
		}
		result.Snapshot.ProxyConfig = append(result.Snapshot.ProxyConfig, access)
	}
	return result, nil
}

func proxyCandidateAllowed(node NodeFacts, fact ProxyFacts, candidate ProxyCandidateFacts, relayByLine map[string]agentruntime.RelayConfig) bool {
	if candidate.LineID == "" || candidate.NodeID != node.ID || candidate.GroupID != node.GroupID || !candidate.LineEnabled {
		return false
	}
	if candidate.HopCount < 1 || candidate.HopCount > 8 || len(candidate.HopGroupIDs) != candidate.HopCount {
		return false
	}
	if candidate.HopCount == 1 {
		if candidate.HopGroupIDs[0] != node.GroupID || candidate.RelayGeneration != 0 {
			return false
		}
	} else {
		if candidate.RelayGeneration == 0 || !candidate.RelayReady || candidate.MaxHops < candidate.HopCount {
			return false
		}
		relay, ok := relayByLine[candidate.LineID]
		if !ok || relay.Role != agentruntime.RelayIngress || relay.Generation != candidate.RelayGeneration {
			return false
		}
	}
	for _, groupID := range candidate.HopGroupIDs {
		if !slices.Contains(fact.MemberGroupIDs, groupID) {
			return false
		}
	}
	if candidate.LineOwnerID == "" {
		return slices.Contains(fact.MemberLineIDs, candidate.LineID)
	}
	return candidate.LineOwnerID == fact.OwnerID && fact.AllowCustomLines
}
