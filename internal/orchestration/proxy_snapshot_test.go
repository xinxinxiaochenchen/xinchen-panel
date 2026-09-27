package orchestration

import (
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentruntime"
)

func TestProxySnapshotFiltersAuthorizationAndRevocations(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPort: 443}
	base := ProxyFacts{ID: "allowed", OwnerID: "user", LineID: "line", NodeID: "node", GroupID: "group",
		CredentialHash: strings.Repeat("a", 56), Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"group"}, MemberLineIDs: []string{"line"}, LineEnabled: true,
		HopCount: 1, HopGroupIDs: []string{"group"},
		ExpiresAt: time.Now().Add(time.Hour)}
	disabled := base
	disabled.ID = "disabled"
	disabled.Enabled = false
	disabled.CredentialHash = strings.Repeat("b", 56)
	ungrouped := base
	ungrouped.ID = "ungrouped"
	ungrouped.CredentialHash = strings.Repeat("c", 56)
	ungrouped.MemberGroupIDs = nil
	compiled, err := CompileProxySnapshot(node, []ProxyFacts{disabled, ungrouped, base}, nil, 1)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 1 || compiled.Snapshot.ProxyConfig[0].ID != "allowed" {
		t.Fatalf("unexpected proxy config: %+v, %v", compiled, err)
	}
	node.Enabled = false
	revoked, err := CompileProxySnapshot(node, []ProxyFacts{base}, nil, 2)
	if err != nil || len(revoked.Snapshot.ProxyConfig) != 0 {
		t.Fatalf("disabled node retained proxy: %+v, %v", revoked, err)
	}
	base.MembershipActive = false
	node.Enabled = true
	revoked, err = CompileProxySnapshot(node, []ProxyFacts{base}, nil, 3)
	if err != nil || len(revoked.Snapshot.ProxyConfig) != 0 {
		t.Fatalf("expired membership retained proxy: %+v, %v", revoked, err)
	}
	base.MembershipActive = true
	for _, mutate := range []func(*ProxyFacts){
		func(f *ProxyFacts) { f.MemberLineIDs = nil },
		func(f *ProxyFacts) { f.LineOwnerID = "other-user"; f.AllowCustomLines = true },
		func(f *ProxyFacts) { f.OwnerActive = false },
		func(f *ProxyFacts) { f.LineEnabled = false },
		func(f *ProxyFacts) { f.ExpiresAt = time.Now().Add(-time.Second) },
	} {
		denied := base
		mutate(&denied)
		got, err := CompileProxySnapshot(node, []ProxyFacts{denied}, nil, 4)
		if err != nil || len(got.Snapshot.ProxyConfig) != 0 {
			t.Fatalf("unauthorized proxy compiled: %+v, %v", got, err)
		}
	}
}

func TestProxySnapshotRejectsMultiHopWithoutRelayGeneration(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPort: 443}
	fact := ProxyFacts{ID: "unsafe", OwnerID: "user", LineID: "line", NodeID: "node", GroupID: "group",
		CredentialHash: strings.Repeat("a", 56), Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"group"}, MemberLineIDs: []string{"line"}, LineEnabled: true,
		HopCount: 2, MaxHops: 2, HopGroupIDs: []string{"group", "group"},
		ExpiresAt: time.Now().Add(time.Hour)}
	compiled, err := CompileProxySnapshot(node, []ProxyFacts{fact}, nil, 1)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 0 || len(compiled.Rejected) != 1 {
		t.Fatalf("multi-hop line fell back to direct proxy: %+v, %v", compiled, err)
	}
}

func TestProxySnapshotRejectsUnknownDirectTopology(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPort: 443}
	fact := ProxyFacts{ID: "unsafe", OwnerID: "user", LineID: "line", NodeID: "node", GroupID: "group",
		CredentialHash: strings.Repeat("a", 56), Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"group"}, MemberLineIDs: []string{"line"}, LineEnabled: true,
		ExpiresAt: time.Now().Add(time.Hour)}
	for _, topology := range []struct {
		hops   int
		groups []string
	}{{0, nil}, {1, nil}, {1, []string{"other"}}} {
		fact.HopCount, fact.HopGroupIDs = topology.hops, topology.groups
		compiled, err := CompileProxySnapshot(node, []ProxyFacts{fact}, nil, 1)
		if err != nil || len(compiled.Snapshot.ProxyConfig) != 0 || len(compiled.Rejected) != 1 {
			t.Fatalf("unknown topology fell back to direct proxy: %+v, %v", compiled, err)
		}
	}
}

func TestProxySnapshotIncludesOnlyReadyMultiHopIngress(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPort: 443}
	base := ProxyFacts{ID: "multi", OwnerID: "user", LineID: "line", NodeID: "node", GroupID: "group",
		CredentialHash: strings.Repeat("a", 56), Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"group"}, MemberLineIDs: []string{"line"}, LineEnabled: true,
		RelayGeneration: 7, RelayReady: true, HopCount: 2, MaxHops: 2, HopGroupIDs: []string{"group", "group"},
		ExpiresAt: time.Now().Add(time.Hour)}
	relay := agentruntime.RelayConfig{LineID: "line", Generation: 7, Role: agentruntime.RelayIngress,
		Next: &agentruntime.RelayNextHop{NodeID: "next", Address: "relay.example.com:24443", Port: 24443,
			Secret: []byte(strings.Repeat("s", 32)), Fingerprints: []string{strings.Repeat("a", 64)}}}
	compiled, err := CompileProxySnapshot(node, []ProxyFacts{base}, []agentruntime.RelayConfig{relay}, 1)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 1 || compiled.Snapshot.ProxyConfig[0].RelayGeneration != 7 {
		t.Fatalf("ready multi-hop proxy missing: %+v, %v", compiled, err)
	}
	base.RelayReady = false
	compiled, err = CompileProxySnapshot(node, []ProxyFacts{base}, []agentruntime.RelayConfig{relay}, 2)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 0 || len(compiled.Rejected) != 1 {
		t.Fatalf("unacknowledged multi-hop proxy was compiled: %+v, %v", compiled, err)
	}
	base.RelayReady = true
	base.HopGroupIDs = []string{"group", "outside"}
	compiled, err = CompileProxySnapshot(node, []ProxyFacts{base}, []agentruntime.RelayConfig{relay}, 3)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 0 {
		t.Fatalf("unauthorized downstream group was compiled: %+v, %v", compiled, err)
	}
	base.HopGroupIDs = []string{"group", "group"}
	base.MaxHops = 1
	compiled, err = CompileProxySnapshot(node, []ProxyFacts{base}, []agentruntime.RelayConfig{relay}, 4)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 0 {
		t.Fatalf("hop limit was ignored: %+v, %v", compiled, err)
	}
}

func TestForwardCompilerReservesProxyTCPPort(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ForwardCapable: true, ProxyCapable: true, ProxyPort: 24443}
	fact := ForwardFacts{ID: "conflict", IngressNodeID: "node", IngressPort: 24443, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP",
		Enabled: true, OwnerActive: true, MembershipActive: true, MemberGroupIDs: []string{"group"}, MaxForwardRulesPerNode: 1, TCPPolicyAllowed: true}
	compiled, err := CompileForwardSnapshot(node, []ForwardFacts{fact}, 1)
	if err != nil || len(compiled.Snapshot.Rules) != 0 || len(compiled.Rejected) != 1 {
		t.Fatalf("proxy port conflict compiled: %+v, %v", compiled, err)
	}
}

func TestProxySnapshotCompilesAuthorizedLineCandidates(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPort: 443}
	base := ProxyFacts{ID: "pool", OwnerID: "user", LineID: "line-a", NodeID: "node", GroupID: "group",
		CredentialHash: strings.Repeat("a", 56), Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"group"}, MemberLineIDs: []string{"line-a", "line-b"}, LineEnabled: true,
		HopCount: 1, HopGroupIDs: []string{"group"}, ExpiresAt: time.Now().Add(time.Hour),
		Candidates: []ProxyCandidateFacts{
			{LineID: "line-a", NodeID: "node", GroupID: "group", LineEnabled: true, HopCount: 1, HopGroupIDs: []string{"group"}, Priority: 10, Weight: 3},
			{LineID: "line-b", NodeID: "node", GroupID: "group", LineEnabled: true, HopCount: 1, HopGroupIDs: []string{"group"}, Priority: 20, Weight: 1},
		}}
	compiled, err := CompileProxySnapshot(node, []ProxyFacts{base}, nil, 1)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 1 {
		t.Fatalf("compiled pool = %+v, %v", compiled, err)
	}
	access := compiled.Snapshot.ProxyConfig[0]
	if len(access.Candidates) != 2 || access.Candidates[0].LineID != "line-a" || access.Candidates[1].LineID != "line-b" {
		t.Fatalf("candidate pool = %+v", access.Candidates)
	}
}

func TestProxySnapshotExcludesRevokedCandidateWithoutLosingPrimary(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPort: 443}
	fact := ProxyFacts{ID: "pool", OwnerID: "user", LineID: "line-a", NodeID: "node", GroupID: "group",
		CredentialHash: strings.Repeat("a", 56), Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"group"}, MemberLineIDs: []string{"line-a"}, LineEnabled: true,
		HopCount: 1, HopGroupIDs: []string{"group"}, ExpiresAt: time.Now().Add(time.Hour),
		Candidates: []ProxyCandidateFacts{
			{LineID: "line-a", NodeID: "node", GroupID: "group", LineEnabled: true, HopCount: 1, HopGroupIDs: []string{"group"}, Weight: 1},
			{LineID: "line-b", NodeID: "node", GroupID: "group", LineEnabled: true, HopCount: 1, HopGroupIDs: []string{"group"}, Weight: 1},
		}}
	compiled, err := CompileProxySnapshot(node, []ProxyFacts{fact}, nil, 1)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 1 || len(compiled.Snapshot.ProxyConfig[0].Candidates) != 1 || compiled.Snapshot.ProxyConfig[0].Candidates[0].LineID != "line-a" {
		t.Fatalf("revoked fallback was compiled: %+v, %v", compiled, err)
	}
	fact.MemberLineIDs = []string{"line-b"}
	compiled, err = CompileProxySnapshot(node, []ProxyFacts{fact}, nil, 2)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 1 || len(compiled.Snapshot.ProxyConfig[0].Candidates) != 1 || compiled.Snapshot.ProxyConfig[0].Candidates[0].LineID != "line-b" {
		t.Fatalf("authorized fallback was not retained: %+v, %v", compiled, err)
	}
}

func TestProxySnapshotExcludesUnappliedRelayCandidate(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPort: 443}
	fact := ProxyFacts{ID: "pool", OwnerID: "user", LineID: "direct", NodeID: "node", GroupID: "group",
		CredentialHash: strings.Repeat("a", 56), Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"group"}, MemberLineIDs: []string{"direct", "relay"}, LineEnabled: true,
		HopCount: 1, HopGroupIDs: []string{"group"}, ExpiresAt: time.Now().Add(time.Hour),
		Candidates: []ProxyCandidateFacts{
			{LineID: "direct", NodeID: "node", GroupID: "group", LineEnabled: true, HopCount: 1, HopGroupIDs: []string{"group"}, Weight: 1},
			{LineID: "relay", NodeID: "node", GroupID: "group", LineEnabled: true, RelayGeneration: 7, RelayReady: true,
				HopCount: 2, MaxHops: 2, HopGroupIDs: []string{"group", "group"}, Weight: 1},
		}}
	compiled, err := CompileProxySnapshot(node, []ProxyFacts{fact}, nil, 1)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 1 || len(compiled.Snapshot.ProxyConfig[0].Candidates) != 1 {
		t.Fatalf("candidate without ingress relay was compiled: %+v, %v", compiled, err)
	}
}

func TestProxySnapshotKeepsAuthorizedFallbackWhenDefaultLineIsDisabled(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPort: 443}
	fact := ProxyFacts{ID: "pool", OwnerID: "user", LineID: "default", NodeID: "node", GroupID: "group",
		CredentialHash: strings.Repeat("a", 56), Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"group"}, MemberLineIDs: []string{"default", "fallback"}, LineEnabled: false,
		HopCount: 1, HopGroupIDs: []string{"group"}, ExpiresAt: time.Now().Add(time.Hour),
		Candidates: []ProxyCandidateFacts{
			{LineID: "default", NodeID: "node", GroupID: "group", LineEnabled: false, HopCount: 1, HopGroupIDs: []string{"group"}, Weight: 1},
			{LineID: "fallback", NodeID: "node", GroupID: "group", LineEnabled: true, HopCount: 1, HopGroupIDs: []string{"group"}, Weight: 1},
		}}
	compiled, err := CompileProxySnapshot(node, []ProxyFacts{fact}, nil, 1)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 1 {
		t.Fatalf("fallback proxy missing: %+v, %v", compiled, err)
	}
	access := compiled.Snapshot.ProxyConfig[0]
	if access.LineID != "default" || len(access.Candidates) != 1 || access.Candidates[0].LineID != "fallback" {
		t.Fatalf("unavailable default remained executable: %+v", access)
	}
}

func TestProxySnapshotKeepsDirectFallbackWhenDefaultRelayIsUnapplied(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPort: 443}
	fact := ProxyFacts{ID: "pool", OwnerID: "user", LineID: "default-relay", NodeID: "node", GroupID: "group",
		CredentialHash: strings.Repeat("a", 56), Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"group"}, MemberLineIDs: []string{"default-relay", "direct"}, LineEnabled: true,
		RelayGeneration: 7, RelayReady: false, HopCount: 2, MaxHops: 2, HopGroupIDs: []string{"group", "group"},
		ExpiresAt: time.Now().Add(time.Hour), Candidates: []ProxyCandidateFacts{
			{LineID: "default-relay", NodeID: "node", GroupID: "group", LineEnabled: true, RelayGeneration: 7,
				RelayReady: false, HopCount: 2, MaxHops: 2, HopGroupIDs: []string{"group", "group"}, Weight: 1},
			{LineID: "direct", NodeID: "node", GroupID: "group", LineEnabled: true, HopCount: 1,
				HopGroupIDs: []string{"group"}, Weight: 1},
		}}
	compiled, err := CompileProxySnapshot(node, []ProxyFacts{fact}, nil, 1)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 1 {
		t.Fatalf("direct fallback missing: %+v, %v", compiled, err)
	}
	access := compiled.Snapshot.ProxyConfig[0]
	if access.RelayGeneration != 0 || len(access.Candidates) != 1 || access.Candidates[0].LineID != "direct" {
		t.Fatalf("unapplied relay remained executable: %+v", access)
	}
}

func TestLegacyAgentReceivesOnlyAvailableDefaultLine(t *testing.T) {
	pool := agentruntime.ProxyAccess{ID: "pool", LineID: "default", RelayGeneration: 0,
		Candidates: []agentruntime.ProxyLineCandidate{{LineID: "default", Weight: 1}, {LineID: "fallback", Weight: 1}}}
	old := proxyAccessesForAgent([]agentruntime.ProxyAccess{pool}, []string{"forward", "proxy"})
	if len(old) != 1 || len(old[0].Candidates) != 0 || old[0].LineID != "default" {
		t.Fatalf("old Agent received candidate pool: %+v", old)
	}
	pool.Candidates = pool.Candidates[1:]
	old = proxyAccessesForAgent([]agentruntime.ProxyAccess{pool}, []string{"forward", "proxy"})
	if len(old) != 0 {
		t.Fatalf("old Agent received unavailable default: %+v", old)
	}
	modern := proxyAccessesForAgent([]agentruntime.ProxyAccess{pool}, []string{"forward", "proxy", "proxy_candidates"})
	if len(modern) != 1 || len(modern[0].Candidates) != 1 || modern[0].Candidates[0].LineID != "fallback" {
		t.Fatalf("candidate-capable Agent lost fallback: %+v", modern)
	}
}
