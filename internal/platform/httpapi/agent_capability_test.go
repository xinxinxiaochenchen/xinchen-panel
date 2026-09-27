package httpapi

import (
	"testing"

	"controlplane/internal/agentruntime"
)

func TestAgentCapabilityGatesProxySnapshot(t *testing.T) {
	snapshot := agentruntime.Snapshot{Revision: 1, ProxyConfig: []agentruntime.ProxyAccess{{ID: "access"}}}
	if err := requireAgentSnapshotCapability([]string{"forward"}, snapshot); err == nil {
		t.Fatal("legacy forward-only Agent received proxy configuration")
	}
	if err := requireAgentSnapshotCapability([]string{"forward", "proxy"}, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := requireAgentSnapshotCapability([]string{"forward"}, agentruntime.Snapshot{Revision: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentCapabilityGatesProxyCandidates(t *testing.T) {
	snapshot := agentruntime.Snapshot{Revision: 1, ProxyConfig: []agentruntime.ProxyAccess{{ID: "access", LineID: "default",
		Candidates: []agentruntime.ProxyLineCandidate{{LineID: "fallback", Weight: 1}}}}}
	if err := requireAgentSnapshotCapability([]string{"forward", "proxy"}, snapshot); err == nil {
		t.Fatal("legacy Agent received explicit candidates")
	}
	if err := requireAgentSnapshotCapability([]string{"forward", "proxy", "proxy_candidates"}, snapshot); err != nil {
		t.Fatal(err)
	}
}
