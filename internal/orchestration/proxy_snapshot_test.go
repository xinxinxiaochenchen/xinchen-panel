package orchestration

import (
	"strings"
	"testing"
	"time"
)

func TestProxySnapshotFiltersAuthorizationAndRevocations(t *testing.T) {
	node := NodeFacts{ID: "node", GroupID: "group", Enabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPort: 443}
	base := ProxyFacts{ID: "allowed", OwnerID: "user", LineID: "line", NodeID: "node", GroupID: "group",
		CredentialHash: strings.Repeat("a", 56), Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"group"}, MemberLineIDs: []string{"line"}, LineEnabled: true,
		ExpiresAt: time.Now().Add(time.Hour)}
	disabled := base
	disabled.ID = "disabled"
	disabled.Enabled = false
	disabled.CredentialHash = strings.Repeat("b", 56)
	ungrouped := base
	ungrouped.ID = "ungrouped"
	ungrouped.CredentialHash = strings.Repeat("c", 56)
	ungrouped.MemberGroupIDs = nil
	compiled, err := CompileProxySnapshot(node, []ProxyFacts{disabled, ungrouped, base}, 1)
	if err != nil || len(compiled.Snapshot.ProxyConfig) != 1 || compiled.Snapshot.ProxyConfig[0].ID != "allowed" {
		t.Fatalf("unexpected proxy config: %+v, %v", compiled, err)
	}
	node.Enabled = false
	revoked, err := CompileProxySnapshot(node, []ProxyFacts{base}, 2)
	if err != nil || len(revoked.Snapshot.ProxyConfig) != 0 {
		t.Fatalf("disabled node retained proxy: %+v, %v", revoked, err)
	}
	base.MembershipActive = false
	node.Enabled = true
	revoked, err = CompileProxySnapshot(node, []ProxyFacts{base}, 3)
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
		got, err := CompileProxySnapshot(node, []ProxyFacts{denied}, 4)
		if err != nil || len(got.Snapshot.ProxyConfig) != 0 {
			t.Fatalf("unauthorized proxy compiled: %+v, %v", got, err)
		}
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
