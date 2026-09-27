package agentruntime

import (
	"bytes"
	"strings"
	"testing"
)

func TestValidateRelayConfigEnforcesRolesAndSecrets(t *testing.T) {
	secret := bytes.Repeat([]byte{1}, 32)
	valid := []RelayConfig{{LineID: "line", Generation: 1, Role: RelayIngress, Next: &RelayNextHop{NodeID: "next", Address: "relay.example.com:24443", Port: 24443, Secret: secret, Fingerprints: []string{strings.Repeat("a", 64)}}}, {LineID: "line2", Generation: 1, Role: RelayMiddle, PreviousNodeID: "prev", PreviousSecret: secret, PreviousFingerprints: []string{strings.Repeat("a", 64)}, Next: &RelayNextHop{NodeID: "next", Address: "relay.example.com:24443", Port: 24443, Secret: secret, Fingerprints: []string{strings.Repeat("a", 64)}}}, {LineID: "line3", Generation: 1, Role: RelayEgress, PreviousNodeID: "prev", PreviousSecret: secret, PreviousFingerprints: []string{strings.Repeat("a", 64)}}}
	if err := ValidateRelayConfig(valid); err != nil {
		t.Fatal(err)
	}
	invalid := []RelayConfig{{LineID: "line", Generation: 1, Role: RelayMiddle, Next: valid[0].Next}, {LineID: "line", Generation: 1, Role: RelayEgress, PreviousNodeID: "p", PreviousSecret: secret}, {LineID: "line", Generation: 1, Role: RelayIngress, Next: &RelayNextHop{NodeID: "n", Address: "a:1", Port: 1, Secret: make([]byte, 32)}}}
	for _, c := range invalid {
		if err := ValidateRelayConfig([]RelayConfig{c}); err == nil {
			t.Fatalf("invalid config accepted: %+v", c)
		}
	}
}
