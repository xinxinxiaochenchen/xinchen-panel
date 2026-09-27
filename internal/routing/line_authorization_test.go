package routing

import (
	"testing"

	"controlplane/internal/entitlement"
)

func TestRoutingLineAuthorizationChecksEveryHop(t *testing.T) {
	grant := entitlement.Snapshot{
		ResourceGroupIDs: []string{"hong-kong", "japan"},
		LineIDs:          []string{"shared-line"},
		Limits:           entitlement.PlanLimits{MaxHops: 2},
	}
	hops := []routingLineHop{
		{Position: 0, Role: "ingress", GroupID: "hong-kong", NodeEnabled: true, GroupEnabled: true, ProxyCapable: true, ForwardCapable: true, ProxyPortReady: true, RelayPortReady: true},
		{Position: 1, Role: "egress", GroupID: "japan", NodeEnabled: true, GroupEnabled: true, ForwardCapable: true, RelayPortReady: true},
	}
	if !routingLineAllowed("user", "shared-line", "", hops, grant) {
		t.Fatal("authorized two-hop shared line was rejected")
	}
	cases := []struct {
		name   string
		change func([]routingLineHop, *entitlement.Snapshot)
	}{
		{"ungranted exit group", func(_ []routingLineHop, g *entitlement.Snapshot) { g.ResourceGroupIDs = []string{"hong-kong"} }},
		{"hop limit", func(_ []routingLineHop, g *entitlement.Snapshot) { g.Limits.MaxHops = 1 }},
		{"disabled exit", func(h []routingLineHop, _ *entitlement.Snapshot) { h[1].NodeEnabled = false }},
		{"missing relay port", func(h []routingLineHop, _ *entitlement.Snapshot) { h[1].RelayPortReady = false }},
		{"wrong exit role", func(h []routingLineHop, _ *entitlement.Snapshot) { h[1].Role = "relay" }},
		{"missing entry proxy", func(h []routingLineHop, _ *entitlement.Snapshot) { h[0].ProxyCapable = false }},
		{"ungranted shared line", func(_ []routingLineHop, g *entitlement.Snapshot) { g.LineIDs = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copyHops := append([]routingLineHop(nil), hops...)
			copyGrant := grant
			tc.change(copyHops, &copyGrant)
			if routingLineAllowed("user", "shared-line", "", copyHops, copyGrant) {
				t.Fatal("unauthorized topology was accepted")
			}
		})
	}
}

func TestRoutingLineAuthorizationPreservesSingleHopAndOwnedLines(t *testing.T) {
	grant := entitlement.Snapshot{ResourceGroupIDs: []string{"japan"}, Limits: entitlement.PlanLimits{AllowCustomLines: true}}
	hops := []routingLineHop{{Position: 0, Role: "egress", GroupID: "japan", NodeEnabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPortReady: true}}
	if !routingLineAllowed("user", "owned-line", "user", hops, grant) {
		t.Fatal("authorized single-hop owned line was rejected")
	}
	if routingLineAllowed("other", "owned-line", "user", hops, grant) {
		t.Fatal("another user's line was accepted")
	}
	grant.Limits.AllowCustomLines = false
	if routingLineAllowed("user", "owned-line", "user", hops, grant) {
		t.Fatal("owned line was accepted after custom lines were revoked")
	}
}
