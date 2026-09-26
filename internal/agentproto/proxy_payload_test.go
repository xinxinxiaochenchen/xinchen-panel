package agentproto

import (
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
