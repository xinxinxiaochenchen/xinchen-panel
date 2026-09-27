package orchestration

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"controlplane/internal/agentruntime"
)

type relaySecretMemory struct {
	values map[string][]byte
	next   byte
}

func (s *relaySecretMemory) GetOrCreate(_ context.Context, line string, generation uint64, edge int) ([]byte, error) {
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	k := relaySecretKey(line, generation, edge)
	if v, ok := s.values[k]; ok {
		return append([]byte(nil), v...), nil
	}
	s.next++
	v := bytes.Repeat([]byte{s.next}, 32)
	s.values[k] = v
	return append([]byte(nil), v...), nil
}
func TestCompileRelaySnapshotsGatesAndSharesOnlyAdjacentSecrets(t *testing.T) {
	store := &relaySecretMemory{}
	facts := RelayLineFacts{LineID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423", Generation: 7, Enabled: true, Hops: []RelayHopFact{
		{NodeID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424", Role: agentruntime.RelayIngress, Host: "in.example.com", RelayPort: 24441, AgentFingerprints: []string{strings.Repeat("a", 64)}, RelayFingerprints: []string{strings.Repeat("b", 64)}, Online: true, RelayCapable: true, ProxyCapable: true},
		{NodeID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e425", Role: agentruntime.RelayMiddle, Host: "mid.example.com", RelayPort: 24442, AgentFingerprints: []string{strings.Repeat("a", 64)}, RelayFingerprints: []string{strings.Repeat("b", 64)}, Online: true, RelayCapable: true},
		{NodeID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e426", Role: agentruntime.RelayEgress, Host: "eg.example.com", RelayPort: 24443, AgentFingerprints: []string{strings.Repeat("a", 64)}, RelayFingerprints: []string{strings.Repeat("b", 64)}, Online: true, RelayCapable: true},
	}}
	compiled, diags, err := CompileRelaySnapshots(context.Background(), []RelayLineFacts{facts}, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 || len(compiled) != 3 {
		t.Fatalf("compiled=%+v diagnostics=%+v", compiled, diags)
	}
	ingress := compiled[facts.Hops[0].NodeID][0]
	middle := compiled[facts.Hops[1].NodeID][0]
	egress := compiled[facts.Hops[2].NodeID][0]
	if ingress.Role != agentruntime.RelayIngress || ingress.Next == nil || len(ingress.Next.Secret) != 32 || len(ingress.PreviousSecret) != 0 {
		t.Fatalf("bad ingress=%+v", ingress)
	}
	if middle.PreviousNodeID != facts.Hops[0].NodeID || middle.Next == nil || !bytes.Equal(middle.PreviousSecret, ingress.Next.Secret) || bytes.Equal(middle.Next.Secret, ingress.Next.Secret) {
		t.Fatalf("bad middle=%+v", middle)
	}
	if egress.PreviousNodeID != facts.Hops[1].NodeID || !bytes.Equal(egress.PreviousSecret, middle.Next.Secret) {
		t.Fatalf("bad egress=%+v", egress)
	}
	again, _, err := CompileRelaySnapshots(context.Background(), []RelayLineFacts{facts}, store)
	if err != nil || !bytes.Equal(again[facts.Hops[0].NodeID][0].Next.Secret, ingress.Next.Secret) {
		t.Fatal("generation secret was not reused")
	}

}
func TestCompileRelaySnapshotsOmitsIncompleteLinesAndReportsReason(t *testing.T) {
	store := &relaySecretMemory{}
	line := RelayLineFacts{LineID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423", Generation: 1, Enabled: true, Hops: []RelayHopFact{{NodeID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424", Role: agentruntime.RelayIngress, Host: "in.example.com", RelayPort: 24441, AgentFingerprints: []string{strings.Repeat("a", 64)}, RelayFingerprints: []string{strings.Repeat("b", 64)}, Online: true, RelayCapable: true, ProxyCapable: true}, {NodeID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e425", Role: agentruntime.RelayEgress, Host: "mid.example.com", RelayPort: 24442, AgentFingerprints: []string{strings.Repeat("a", 64)}, RelayFingerprints: []string{strings.Repeat("b", 64)}, Online: false, RelayCapable: true}}}
	got, diag, err := CompileRelaySnapshots(context.Background(), []RelayLineFacts{line}, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || len(diag) != 1 || !strings.Contains(diag[0].Reason, "online") {
		t.Fatalf("got=%v diag=%v", got, diag)
	}
	line.Enabled = false
	_, diag, err = CompileRelaySnapshots(context.Background(), []RelayLineFacts{line}, store)
	if err != nil || len(diag) != 1 {
		t.Fatalf("disabled line diag=%v err=%v", diag, err)
	}
}
func TestCompileRelaySnapshotsRejectsSecretStoreFailure(t *testing.T) {
	store := failingRelaySecretStore{}
	line := RelayLineFacts{LineID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423", Generation: 1, Enabled: true, Hops: []RelayHopFact{{NodeID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424", Role: agentruntime.RelayIngress, Host: "in.example.com", RelayPort: 24441, AgentFingerprints: []string{strings.Repeat("a", 64)}, RelayFingerprints: []string{strings.Repeat("b", 64)}, Online: true, RelayCapable: true, ProxyCapable: true}, {NodeID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e425", Role: agentruntime.RelayEgress, Host: "eg.example.com", RelayPort: 24442, AgentFingerprints: []string{strings.Repeat("a", 64)}, RelayFingerprints: []string{strings.Repeat("b", 64)}, Online: true, RelayCapable: true}}}
	if _, _, err := CompileRelaySnapshots(context.Background(), []RelayLineFacts{line}, store); !errors.Is(err, errSecretStore) {
		t.Fatalf("error=%v", err)
	}
}

var _ = bytes.Equal
