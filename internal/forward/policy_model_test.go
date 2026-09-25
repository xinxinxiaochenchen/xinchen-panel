package forward

import (
	"errors"
	"testing"
)

func TestNormalizeTargetPolicy(t *testing.T) {
	for _, kind := range []string{"public_host", "node"} {
		input := NewTargetPolicy{Kind: kind, Protocol: "TCP", PortStart: 20000, PortEnd: 30000}
		if kind == "node" {
			group := testIngressID
			input.TargetGroupID = &group
		}
		got, err := NormalizeTargetPolicy(input)
		if err != nil || got.Kind != kind || got.Protocol != "TCP" || !got.Enabled {
			t.Fatalf("policy %s = %+v, %v", kind, got, err)
		}
	}
	group := testIngressID
	for _, tc := range []struct {
		name  string
		input NewTargetPolicy
		field string
	}{
		{"unknown kind", NewTargetPolicy{Kind: "anything", Protocol: "TCP", PortStart: 1, PortEnd: 2}, "kind"},
		{"public host with group", NewTargetPolicy{Kind: "public_host", TargetGroupID: &group, Protocol: "TCP", PortStart: 1, PortEnd: 2}, "target_group_id"},
		{"node without group", NewTargetPolicy{Kind: "node", Protocol: "TCP", PortStart: 1, PortEnd: 2}, "target_group_id"},
		{"BOTH policy", NewTargetPolicy{Kind: "public_host", Protocol: "BOTH", PortStart: 1, PortEnd: 2}, "protocol"},
		{"zero port", NewTargetPolicy{Kind: "public_host", Protocol: "UDP", PortStart: 0, PortEnd: 2}, "port_start"},
		{"reversed ports", NewTargetPolicy{Kind: "public_host", Protocol: "UDP", PortStart: 200, PortEnd: 100}, "port_end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeTargetPolicy(tc.input)
			var validation ValidationError
			if !errors.As(err, &validation) || validation.Field != tc.field {
				t.Fatalf("error = %v; want %s", err, tc.field)
			}
		})
	}
}

func TestNormalizeTargetPolicyPatch(t *testing.T) {
	if _, err := NormalizeTargetPolicyPatch(TargetPolicyPatch{}); err == nil {
		t.Fatal("empty patch accepted")
	}
	value := false
	if _, err := NormalizeTargetPolicyPatch(TargetPolicyPatch{Enabled: &value}); err != nil {
		t.Fatal(err)
	}
}
