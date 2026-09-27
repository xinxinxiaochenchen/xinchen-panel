package agentruntime

import (
	"bytes"
	"testing"
)

func TestValidateRelayConfigEnforcesRolesAndSecrets(t *testing.T) {
	secret := bytes.Repeat([]byte{1}, 32)
	valid := []RelayConfig{{LineID: "line", Generation: 1, Role: RelayIngress, Next: &RelayNextHop{NodeID: "next", Address: "relay.example.com:24443", Port: 24443, Secret: secret}}, {LineID: "line2", Generation: 1, Role: RelayMiddle, PreviousNodeID: "prev", PreviousSecret: secret, Next: &RelayNextHop{NodeID: "next", Address: "relay.example.com:24443", Port: 24443, Secret: secret}}, {LineID: "line3", Generation: 1, Role: RelayEgress, PreviousNodeID: "prev", PreviousSecret: secret, TargetHost: "example.com", TargetPort: 443}}
	if err := ValidateRelayConfig(valid); err != nil {
		t.Fatal(err)
	}
	invalid := []RelayConfig{{LineID: "line", Generation: 1, Role: RelayMiddle, Next: valid[0].Next}, {LineID: "line", Generation: 1, Role: RelayEgress, PreviousNodeID: "p", PreviousSecret: secret, TargetHost: "127.0.0.1", TargetPort: 443}, {LineID: "line", Generation: 1, Role: RelayIngress, Next: &RelayNextHop{NodeID: "n", Address: "a:1", Port: 1, Secret: make([]byte, 32)}}}
	for _, c := range invalid {
		if err := ValidateRelayConfig([]RelayConfig{c}); err == nil {
			t.Fatalf("invalid config accepted: %+v", c)
		}
	}
}
