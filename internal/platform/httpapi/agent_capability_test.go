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
