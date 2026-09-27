package entitlement

import "testing"

func TestSharedLineGrantAllowsAuthorizedMultihopTopology(t *testing.T) {
	hops := []planLineHop{
		{Position: 0, Role: "ingress", GroupID: "hk", NodeEnabled: true, GroupEnabled: true, ProxyCapable: true, ForwardCapable: true, ProxyPortReady: true, RelayPortReady: true},
		{Position: 1, Role: "relay", GroupID: "jp", NodeEnabled: true, GroupEnabled: true, ForwardCapable: true, RelayPortReady: true},
		{Position: 2, Role: "egress", GroupID: "us", NodeEnabled: true, GroupEnabled: true, ForwardCapable: true, RelayPortReady: true},
	}
	if !planLineGrantAllowed(hops, []string{"hk", "jp", "us"}, 3) {
		t.Fatal("authorized multihop line was rejected")
	}
	for _, mutate := range []func([]planLineHop, *[]string, *int){
		func(h []planLineHop, _ *[]string, _ *int) { h[1].RelayPortReady = false },
		func(h []planLineHop, _ *[]string, _ *int) { h[0].ProxyCapable = false },
		func(h []planLineHop, groups *[]string, _ *int) { *groups = []string{"hk", "jp"} },
		func(_ []planLineHop, _ *[]string, max *int) { *max = 2 },
		func(h []planLineHop, _ *[]string, _ *int) { h[2].Role = "relay" },
	} {
		candidate := append([]planLineHop(nil), hops...)
		groups := []string{"hk", "jp", "us"}
		maxHops := 3
		mutate(candidate, &groups, &maxHops)
		if planLineGrantAllowed(candidate, groups, maxHops) {
			t.Fatal("invalid multihop grant topology was accepted")
		}
	}
}

func TestSharedLineGrantPreservesSingleHopRules(t *testing.T) {
	hop := []planLineHop{{Position: 0, Role: "egress", GroupID: "jp", NodeEnabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPortReady: true}}
	if !planLineGrantAllowed(hop, []string{"jp"}, 1) {
		t.Fatal("valid single-hop line was rejected")
	}
	if planLineGrantAllowed(hop, []string{"us"}, 1) {
		t.Fatal("line outside granted group was accepted")
	}
}
