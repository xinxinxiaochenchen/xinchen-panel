package orchestration

import (
	"testing"

	"controlplane/internal/agentruntime"
)

func TestCanonicalForwardPayloadIgnoresOrderRevisionAndDiagnostics(t *testing.T) {
	first := agentruntime.Rule{ID: "first", IngressPort: 24000, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true}
	second := agentruntime.Rule{ID: "second", IngressPort: 24001, TargetHost: "example.org", TargetPort: 443, Protocol: "UDP", Enabled: true}
	left := CompiledForwardSnapshot{Snapshot: agentruntime.Snapshot{Revision: 1, Rules: []agentruntime.Rule{first, second}}}
	right := CompiledForwardSnapshot{Snapshot: agentruntime.Snapshot{Revision: 8, Rules: []agentruntime.Rule{second, first}},
		Rejected: []ForwardRejection{{RuleID: "invalid", Reason: "revoked"}}}
	leftJSON, leftHash, err := CanonicalForwardPayload(left)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, rightHash, err := CanonicalForwardPayload(right)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftJSON) != string(rightJSON) || leftHash != rightHash {
		t.Fatalf("equivalent executable payloads differ: %q %s vs %q %s", leftJSON, leftHash, rightJSON, rightHash)
	}
	if len(leftHash) != 64 {
		t.Fatalf("SHA-256 digest length = %d", len(leftHash))
	}
	right.Snapshot.Rules[0].TargetPort = 8443
	_, changedHash, err := CanonicalForwardPayload(right)
	if err != nil || changedHash == leftHash {
		t.Fatalf("changed executable payload digest = %s, %v", changedHash, err)
	}
}
