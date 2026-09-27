package forward

import (
	"errors"
	"strings"
	"testing"
)

const testIngressID = "11111111-1111-7111-8111-111111111111"
const testTargetID = "22222222-2222-7222-8222-222222222222"

func TestNormalizeRuleAcceptsDirectTargetsAndProtocols(t *testing.T) {
	for _, protocol := range []string{"TCP", "UDP", "BOTH"} {
		host := " ExAmPlE.Org. "
		got, err := NormalizeRule(NewRule{Name: " game ", IngressNodeID: testIngressID,
			IngressPort: 24000, TargetHost: &host, TargetPort: 443, Protocol: protocol})
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "game" || got.TargetHost == nil || *got.TargetHost != "example.org" || got.Protocol != protocol || !got.Enabled {
			t.Fatalf("normalized %s rule = %+v", protocol, got)
		}
	}
	target := testTargetID
	disabled := false
	got, err := NormalizeRule(NewRule{Name: "to node", IngressNodeID: testIngressID,
		IngressPort: 24001, TargetNodeID: &target, TargetPort: 8080, Protocol: "TCP", Enabled: &disabled})
	if err != nil || got.TargetNodeID == nil || got.TargetHost != nil || got.Enabled {
		t.Fatalf("node target = %+v, %v", got, err)
	}
}

func TestNormalizeRuleAcceptsLineBoundPublicTarget(t *testing.T) {
	host := "example.org"
	line := "44444444-4444-4444-8444-444444444444"
	got, err := NormalizeRule(NewRule{Name: "relay dns", IngressNodeID: testIngressID, IngressPort: 24001,
		TargetHost: &host, TargetPort: 53, Protocol: "UDP", LineID: &line})
	if err != nil || got.LineID == nil || *got.LineID != line {
		t.Fatalf("line-bound forward=%+v, err=%v", got, err)
	}
	target := testTargetID
	if _, err := NormalizeRule(NewRule{Name: "bad line target", IngressNodeID: testIngressID, IngressPort: 24001,
		TargetNodeID: &target, TargetPort: 53, Protocol: "UDP", LineID: &line}); err == nil {
		t.Fatal("line-bound node target accepted")
	}
}

func TestNormalizeRuleRejectsInvalidTargetsAndPorts(t *testing.T) {
	base := NewRule{Name: "game", IngressNodeID: testIngressID,
		IngressPort: 24000, TargetPort: 443, Protocol: "TCP"}
	host := "example.org"
	target := testTargetID
	cases := []struct {
		name  string
		edit  func(*NewRule)
		field string
	}{
		{"missing target", func(*NewRule) {}, "target"},
		{"dual target", func(v *NewRule) { v.TargetHost, v.TargetNodeID = &host, &target }, "target"},
		{"invalid ingress", func(v *NewRule) { v.IngressNodeID = "bad" }, "ingress_node_id"},
		{"invalid target node", func(v *NewRule) { v.TargetNodeID = &[]string{"bad"}[0] }, "target_node_id"},
		{"private IPv4", func(v *NewRule) { v.TargetHost = &[]string{"10.1.2.3"}[0] }, "target_host"},
		{"metadata IPv4", func(v *NewRule) { v.TargetHost = &[]string{"169.254.169.254"}[0] }, "target_host"},
		{"private IPv6", func(v *NewRule) { v.TargetHost = &[]string{"fd00::1"}[0] }, "target_host"},
		{"localhost", func(v *NewRule) { v.TargetHost = &[]string{"localhost"}[0] }, "target_host"},
		{"URL", func(v *NewRule) { v.TargetHost = &[]string{"https://example.org"}[0] }, "target_host"},
		{"privileged ingress", func(v *NewRule) { v.TargetHost, v.IngressPort = &host, 443 }, "ingress_port"},
		{"invalid destination port", func(v *NewRule) { v.TargetHost, v.TargetPort = &host, 0 }, "target_port"},
		{"invalid protocol", func(v *NewRule) { v.TargetHost, v.Protocol = &host, "ICMP" }, "protocol"},
		{"empty name", func(v *NewRule) { v.TargetHost, v.Name = &host, " " }, "name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			tc.edit(&input)
			_, err := NormalizeRule(input)
			var validation ValidationError
			if !errors.As(err, &validation) || validation.Field != tc.field {
				t.Fatalf("error = %v; want field %s", err, tc.field)
			}
		})
	}
}

func TestNormalizeRuleCountsUnicodeCharacters(t *testing.T) {
	host := "example.org"
	input := NewRule{Name: strings.Repeat("中", 100), IngressNodeID: testIngressID,
		IngressPort: 24000, TargetHost: &host, TargetPort: 443, Protocol: "TCP"}
	if _, err := NormalizeRule(input); err != nil {
		t.Fatalf("100 Unicode characters rejected: %v", err)
	}
	input.Name += "中"
	if _, err := NormalizeRule(input); err == nil {
		t.Fatal("101 Unicode characters accepted")
	}
}

func TestNormalizeRulePatch(t *testing.T) {
	if _, err := NormalizeRulePatch(RulePatch{}); err == nil {
		t.Fatal("empty patch accepted")
	}
	empty := "  "
	if _, err := NormalizeRulePatch(RulePatch{Name: &empty}); err == nil {
		t.Fatal("blank name accepted")
	}
	name := " renamed "
	enabled := false
	got, err := NormalizeRulePatch(RulePatch{Name: &name, Enabled: &enabled})
	if err != nil || got.Name == nil || *got.Name != "renamed" || got.Enabled == nil || *got.Enabled {
		t.Fatalf("patch = %+v, %v", got, err)
	}
}
