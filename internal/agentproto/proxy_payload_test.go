package agentproto

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentruntime"
)

func TestProxyConfigurationDigestAndDecode(t *testing.T) {
	first := agentruntime.ProxyAccess{ID: "first", UserID: "user", LineID: "line", IngressPort: 24443,
		CredentialHash: strings.Repeat("a", 56), ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
	second := first
	second.ID = "second"
	second.CredentialHash = strings.Repeat("b", 56)
	_, digest, err := CanonicalConfig(nil, []agentruntime.ProxyAccess{second, first})
	if err != nil {
		t.Fatal(err)
	}
	_, reordered, err := CanonicalConfig(nil, []agentruntime.ProxyAccess{first, second})
	if err != nil || reordered != digest {
		t.Fatalf("order-sensitive digest: %v", err)
	}
	value := ConfigSnapshot{Revision: 1, SHA256: digest, ValidUntil: time.Now().Add(time.Minute), ProxyConfig: []agentruntime.ProxyAccess{first, second}}
	encoded, _ := json.Marshal(value)
	if decoded, err := DecodeConfigSnapshot(encoded, time.Now()); err != nil || len(decoded.ProxyConfig) != 2 {
		t.Fatalf("decode proxy snapshot: %+v, %v", decoded, err)
	}
	value.ProxyConfig[1].CredentialHash = strings.Repeat("c", 56)
	encoded, _ = json.Marshal(value)
	if _, err := DecodeConfigSnapshot(encoded, time.Now()); err == nil {
		t.Fatal("changed proxy hash accepted old digest")
	}
}

func TestRelayConfigParticipatesInCanonicalDigestAndStrictDecode(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	relay := []agentruntime.RelayConfig{{LineID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423", Generation: 2, Role: agentruntime.RelayEgress, PreviousNodeID: "prev", PreviousSecret: secret, TargetHost: "example.com", TargetPort: 443}}
	payload, digest, err := CanonicalConfigWithRelay(nil, nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(ConfigSnapshot{Revision: 1, SHA256: digest, ValidUntil: time.Now().Add(time.Hour), RelayConfig: relay})
	decoded, err := DecodeConfigSnapshot(encoded, time.Now())
	if err != nil || len(decoded.RelayConfig) != 1 {
		t.Fatalf("relay decode=%+v %v", decoded, err)
	}
	if _, digest2, err := CanonicalConfigWithRelay(nil, nil, nil); err != nil || digest2 == digest {
		t.Fatal("relay config did not affect digest")
	}
	_ = payload
}
