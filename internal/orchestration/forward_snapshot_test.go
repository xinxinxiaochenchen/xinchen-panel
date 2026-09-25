package orchestration

import (
	"testing"
)

func validForwardFacts() ForwardFacts {
	return ForwardFacts{
		ID: "rule-b", IngressNodeID: "node-1", IngressPort: 24000, TargetHost: "example.org", TargetPort: 443,
		Protocol: "TCP", Enabled: true, OwnerActive: true, MembershipActive: true,
		MemberGroupIDs: []string{"ingress", "target"}, MaxForwardRulesPerNode: 1, TCPPolicyAllowed: true,
	}
}

func TestCompileForwardSnapshotFiltersRevokedResources(t *testing.T) {
	node := NodeFacts{ID: "node-1", GroupID: "ingress", Enabled: true, GroupEnabled: true, ForwardCapable: true}
	valid := validForwardFacts()
	first := valid
	first.ID = "rule-a"
	first.IngressPort = 24001
	invalidOwner := valid
	invalidOwner.ID = "owner-disabled"
	invalidOwner.OwnerActive = false
	invalidMember := valid
	invalidMember.ID = "membership-expired"
	invalidMember.MembershipActive = false
	zeroLimit := valid
	zeroLimit.ID = "limit-revoked"
	zeroLimit.MaxForwardRulesPerNode = 0
	invalidPolicy := valid
	invalidPolicy.ID = "policy-revoked"
	invalidPolicy.TCPPolicyAllowed = false
	invalidGroup := valid
	invalidGroup.ID = "group-revoked"
	invalidGroup.MemberGroupIDs = []string{"target"}
	invalidTarget := valid
	invalidTarget.ID = "target-disabled"
	invalidTarget.TargetNodeID = "target-node"
	invalidTarget.TargetNodeEnabled = false
	invalidTarget.TargetGroupEnabled = true
	invalidTarget.TargetGroupID = "target"
	compiled, err := CompileForwardSnapshot(node, []ForwardFacts{valid, invalidOwner, invalidMember, zeroLimit, invalidPolicy,
		invalidGroup, invalidTarget, first}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Snapshot.Revision != 7 || len(compiled.Snapshot.Rules) != 2 || compiled.Snapshot.Rules[0].ID != "rule-a" ||
		compiled.Snapshot.Rules[1].ID != "rule-b" {
		t.Fatalf("compiled snapshot = %+v", compiled)
	}
}

func TestCompileForwardSnapshotRequiresBothProtocolPoliciesAndTargetGrant(t *testing.T) {
	node := NodeFacts{ID: "node-1", GroupID: "ingress", Enabled: true, GroupEnabled: true, ForwardCapable: true}
	rule := validForwardFacts()
	rule.Protocol = "BOTH"
	rule.TargetNodeID = "target-node"
	rule.TargetNodeEnabled = true
	rule.TargetGroupEnabled = true
	rule.TargetGroupID = "target"
	for _, mutate := range []func(*ForwardFacts){
		func(rule *ForwardFacts) { rule.UDPPolicyAllowed = false },
		func(rule *ForwardFacts) { rule.MemberGroupIDs = []string{"ingress"} },
		func(rule *ForwardFacts) { rule.TargetGroupEnabled = false },
	} {
		candidate := rule
		candidate.UDPPolicyAllowed = true
		mutate(&candidate)
		compiled, err := CompileForwardSnapshot(node, []ForwardFacts{candidate}, 1)
		if err != nil || len(compiled.Snapshot.Rules) != 0 {
			t.Fatalf("revoked target or protocol compiled: %+v, %v", compiled, err)
		}
	}
	rule.UDPPolicyAllowed = true
	compiled, err := CompileForwardSnapshot(node, []ForwardFacts{rule}, 1)
	if err != nil || len(compiled.Snapshot.Rules) != 1 {
		t.Fatalf("authorized BOTH rule missing: %+v, %v", compiled, err)
	}
}

func TestCompileForwardSnapshotFailsClosedOnInvalidAndDuplicateRules(t *testing.T) {
	node := NodeFacts{ID: "node-1", GroupID: "ingress", Enabled: true, GroupEnabled: true, ForwardCapable: true}
	rule := validForwardFacts()
	rule.TargetHost = "127.0.0.1"
	compiled, err := CompileForwardSnapshot(node, []ForwardFacts{rule}, 1)
	if err != nil || len(compiled.Snapshot.Rules) != 0 || len(compiled.Rejected) != 1 || compiled.Rejected[0].RuleID != rule.ID {
		t.Fatalf("private target was not individually rejected: %+v, %v", compiled, err)
	}
	rule.TargetHost = "example.org"
	other := rule
	other.ID = "duplicate-listener"
	compiled, err = CompileForwardSnapshot(node, []ForwardFacts{rule, other}, 1)
	if err != nil || len(compiled.Snapshot.Rules) != 1 || len(compiled.Rejected) != 1 ||
		compiled.Rejected[0].RuleID != "rule-b" {
		t.Fatalf("duplicate physical listener was not deterministically rejected: %+v, %v", compiled, err)
	}
	if _, err := CompileForwardSnapshot(node, nil, 0); err == nil {
		t.Fatal("zero revision compiled")
	}
	node.Enabled = false
	compiled, err = CompileForwardSnapshot(node, []ForwardFacts{rule}, 1)
	if err != nil || len(compiled.Snapshot.Rules) != 0 {
		t.Fatalf("disabled node retained rules: %+v, %v", compiled, err)
	}
}

func TestCompileForwardSnapshotKeepsRevocationsDespiteMalformedRule(t *testing.T) {
	node := NodeFacts{ID: "node-1", GroupID: "ingress", Enabled: true, GroupEnabled: true, ForwardCapable: true}
	valid := validForwardFacts()
	malformed := valid
	malformed.ID = "malformed"
	malformed.IngressPort = 24002
	malformed.TargetHost = "127.0.0.1"
	revoked := valid
	revoked.ID = "revoked"
	revoked.IngressPort = 24003
	revoked.TCPPolicyAllowed = false
	compiled, err := CompileForwardSnapshot(node, []ForwardFacts{valid, malformed, revoked}, 8)
	if err != nil || len(compiled.Snapshot.Rules) != 1 || compiled.Snapshot.Rules[0].ID != valid.ID ||
		len(compiled.Rejected) != 1 || compiled.Rejected[0].RuleID != malformed.ID {
		t.Fatalf("malformed rule blocked revocation: %+v, %v", compiled, err)
	}
}
