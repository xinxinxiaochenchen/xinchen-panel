package agentproto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentruntime"
)

func testForwardRule() agentruntime.Rule {
	return agentruntime.Rule{ID: "rule-1", IngressPort: 24000, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true}
}

func TestForwardConfigDigestIsOrderIndependent(t *testing.T) {
	second := testForwardRule()
	second.ID = "rule-2"
	second.IngressPort = 24001
	left, firstDigest, err := CanonicalForwardConfig([]agentruntime.Rule{second, testForwardRule()})
	if err != nil {
		t.Fatal(err)
	}
	right, secondDigest, err := CanonicalForwardConfig([]agentruntime.Rule{testForwardRule(), second})
	if err != nil || string(left) != string(right) || firstDigest != secondDigest {
		t.Fatalf("order changed executable config: %s %s %v", firstDigest, secondDigest, err)
	}
	if !strings.Contains(string(left), `"forward_config"`) {
		t.Fatalf("unexpected executable shape: %s", left)
	}
}

func TestCanonicalConfigAcceptsForwardBoundToIngressRelay(t *testing.T) {
	line := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = 7
	}
	rule := agentruntime.Rule{ID: "forward", IngressPort: 24000, TargetHost: "example.org", TargetPort: 53,
		Protocol: "UDP", Enabled: true, LineID: line, RelayGeneration: 1}
	relay := agentruntime.RelayConfig{LineID: line, Generation: 1, Role: agentruntime.RelayIngress,
		Next: &agentruntime.RelayNextHop{NodeID: "33333333-3333-4333-8333-333333333333",
			Address: "relay.example.org:24443", Port: 24443, Secret: secret, Fingerprints: []string{strings.Repeat("a", 64)}}}
	if _, _, err := CanonicalConfigWithRelay([]agentruntime.Rule{rule}, nil, []agentruntime.RelayConfig{relay}); err != nil {
		t.Fatalf("canonical line-bound forward rejected: %v", err)
	}
}

func TestDecodeConfigSnapshotChecksDigestAndExpiry(t *testing.T) {
	rule := testForwardRule()
	_, digest, err := CanonicalForwardConfig([]agentruntime.Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 1, 2, 3, 0, time.UTC)
	value := ConfigSnapshot{Revision: 2, SHA256: digest, ValidUntil: now.Add(time.Minute), ForwardConfig: []agentruntime.Rule{rule}}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeConfigSnapshot(data, now)
	if err != nil || decoded.Revision != 2 || len(decoded.ForwardConfig) != 1 {
		t.Fatalf("valid snapshot = %+v, %v", decoded, err)
	}
	value.ForwardConfig[0].TargetPort = 8443
	data, _ = json.Marshal(value)
	if _, err := DecodeConfigSnapshot(data, now); err == nil {
		t.Fatal("accepted corrupted snapshot")
	}
	value.ForwardConfig[0].TargetPort = 443
	value.ValidUntil = now
	data, _ = json.Marshal(value)
	if _, err := DecodeConfigSnapshot(data, now); err == nil {
		t.Fatal("accepted expired snapshot")
	}
	value.ValidUntil = now.Add(time.Minute)
	value.ForwardConfig[0].Enabled = false
	data, _ = json.Marshal(value)
	if _, err := DecodeConfigSnapshot(data, now); err == nil {
		t.Fatal("accepted disabled executable rule")
	}
}

func TestDecodeConfigResultBoundsAgentError(t *testing.T) {
	value := ConfigResult{Revision: 3, SHA256: strings.Repeat("a", 64), Status: "rejected", ErrorCode: "BIND_FAILED", ErrorMessage: "port busy"}
	data, _ := json.Marshal(value)
	if _, err := DecodeConfigResult(data); err != nil {
		t.Fatal(err)
	}
	value.ErrorMessage = strings.Repeat("x", 1025)
	data, _ = json.Marshal(value)
	if _, err := DecodeConfigResult(data); err == nil {
		t.Fatal("accepted oversized error message")
	}
	value.ErrorMessage = "port busy"
	value.ErrorCode = "bad\ncode"
	data, _ = json.Marshal(value)
	if _, err := DecodeConfigResult(data); err == nil {
		t.Fatal("accepted unsafe error code")
	}
	value.ErrorCode = ""
	value.Status = "applied"
	data, _ = json.Marshal(value)
	if _, err := DecodeConfigResult(data); err == nil {
		t.Fatal("accepted applied result with error message")
	}
}
