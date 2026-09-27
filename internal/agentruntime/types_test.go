package agentruntime

import (
	"context"
	"net/netip"
	"testing"
)

func TestValidateSnapshotRequiresMatchingIngressForRelayedForward(t *testing.T) {
	rule := Rule{ID: "forward-1", IngressPort: 24000, TargetHost: "example.org", TargetPort: 53,
		Protocol: "UDP", Enabled: true, LineID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423", RelayGeneration: 1}
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, Rules: []Rule{rule}}); err == nil {
		t.Fatal("relayed forward accepted without matching ingress route")
	}
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, Rules: []Rule{rule}, RelayConfig: []RelayConfig{relayTestConfig(RelayIngress)}}); err != nil {
		t.Fatalf("matching relayed forward rejected: %v", err)
	}
	rule.RelayGeneration = 0
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, Rules: []Rule{rule}}); err == nil {
		t.Fatal("line-bound forward accepted without generation")
	}
}

func TestValidateSnapshot(t *testing.T) {
	base := Snapshot{Revision: 1, Rules: []Rule{{ID: "rule-1", IngressPort: 24000,
		TargetHost: "example.org", TargetPort: 443, Protocol: "BOTH", Enabled: true}}}
	keys, err := ValidateSnapshot(base)
	if err != nil || len(keys) != 2 {
		t.Fatalf("valid BOTH snapshot = %v, %v", keys, err)
	}
	if _, ok := keys[listenerKey{protocol: "TCP", port: 24000}]; !ok {
		t.Fatal("TCP listener missing")
	}
	if _, ok := keys[listenerKey{protocol: "UDP", port: 24000}]; !ok {
		t.Fatal("UDP listener missing")
	}
	for _, tc := range []struct {
		name string
		edit func(*Snapshot)
	}{
		{"zero revision", func(s *Snapshot) { s.Revision = 0 }},
		{"missing ID", func(s *Snapshot) { s.Rules[0].ID = "" }},
		{"privileged ingress", func(s *Snapshot) { s.Rules[0].IngressPort = 443 }},
		{"invalid target port", func(s *Snapshot) { s.Rules[0].TargetPort = 0 }},
		{"private target", func(s *Snapshot) { s.Rules[0].TargetHost = "10.0.0.1" }},
		{"local hostname", func(s *Snapshot) { s.Rules[0].TargetHost = "localhost" }},
		{"invalid protocol", func(s *Snapshot) { s.Rules[0].Protocol = "SCTP" }},
		{"duplicate TCP port", func(s *Snapshot) {
			s.Rules = append(s.Rules, Rule{ID: "rule-2", IngressPort: 24000, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true})
		}},
		{"duplicate rule ID", func(s *Snapshot) {
			s.Rules = append(s.Rules, Rule{ID: "rule-1", IngressPort: 24001, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := base
			snapshot.Rules = append([]Rule(nil), base.Rules...)
			tc.edit(&snapshot)
			if _, err := ValidateSnapshot(snapshot); err == nil {
				t.Fatalf("invalid snapshot accepted: %+v", snapshot)
			}
		})
	}
	disabled := base
	disabled.Rules = []Rule{{ID: "rule-1", IngressPort: 24000, Protocol: "TCP", Enabled: false}}
	if keys, err := ValidateSnapshot(disabled); err != nil || len(keys) != 0 {
		t.Fatalf("disabled rule should not create listener: %v, %v", keys, err)
	}
}

func TestResolvePublicRejectsPrivateDNSResults(t *testing.T) {
	resolve := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.1.2.3"), netip.MustParseAddr("203.0.113.4"), netip.MustParseAddr("8.8.8.8")}, nil
	}
	got, err := ResolvePublic(context.Background(), "example.org", resolve)
	if err != nil || got.String() != "8.8.8.8" {
		t.Fatalf("public candidate = %s, %v", got, err)
	}
	privateOnly := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("169.254.169.254")}, nil
	}
	if _, err := ResolvePublic(context.Background(), "example.org", privateOnly); err == nil {
		t.Fatal("private DNS answers accepted")
	}
	if _, err := ResolvePublic(context.Background(), "192.168.1.1", resolve); err == nil {
		t.Fatal("private IP literal accepted")
	}
}
