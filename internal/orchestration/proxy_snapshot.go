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
	ExpiresAt        time.Time
	EligibilityError string
}

type CompiledProxySnapshot struct {
	Snapshot agentruntime.Snapshot
	Rejected []ForwardRejection
}

func CompileProxySnapshot(node NodeFacts, facts []ProxyFacts, revision uint64) (CompiledProxySnapshot, error) {
	result := CompiledProxySnapshot{Snapshot: agentruntime.Snapshot{Revision: revision, ProxyConfig: make([]agentruntime.ProxyAccess, 0)}}
	if !node.Enabled || !node.GroupEnabled || !node.ProxyCapable || node.ProxyPort < 1 || node.ProxyPort > 65535 {
		return result, nil
	}
	ordered := append([]ProxyFacts(nil), facts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, fact := range ordered {
		if fact.NodeID != node.ID || fact.GroupID != node.GroupID {
			result.Rejected = append(result.Rejected, ForwardRejection{RuleID: fact.ID, Reason: "proxy belongs to another node or resource group"})
			continue
		}
		if fact.EligibilityError != "" {
			result.Rejected = append(result.Rejected, ForwardRejection{RuleID: fact.ID, Reason: fact.EligibilityError})
			continue
		}
		if !fact.Enabled || !fact.OwnerActive || !fact.MembershipActive || !fact.LineEnabled ||
			!slices.Contains(fact.MemberGroupIDs, node.GroupID) || !fact.ExpiresAt.After(time.Now()) {
			continue
		}
		if fact.LineOwnerID == "" {
			if !slices.Contains(fact.MemberLineIDs, fact.LineID) {
				continue
			}
		} else if fact.LineOwnerID != fact.OwnerID || !fact.AllowCustomLines {
			continue
		}
		access := agentruntime.ProxyAccess{ID: fact.ID, UserID: fact.OwnerID, LineID: fact.LineID,
			IngressPort: node.ProxyPort, CredentialHash: fact.CredentialHash, ExpiresAt: fact.ExpiresAt}
		trial := append(append([]agentruntime.ProxyAccess(nil), result.Snapshot.ProxyConfig...), access)
		if _, err := agentruntime.ValidateSnapshot(agentruntime.Snapshot{Revision: revision, ProxyConfig: trial}); err != nil {
			result.Rejected = append(result.Rejected, ForwardRejection{RuleID: fact.ID, Reason: err.Error()})
			continue
		}
		result.Snapshot.ProxyConfig = append(result.Snapshot.ProxyConfig, access)
	}
	return result, nil
}
