package agentruntime

import (
	"strings"
	"testing"
	"time"
)

func testProxyConfig() ProxyAccess {
	return ProxyAccess{ID: "access", UserID: "user", LineID: "line", IngressPort: 24443,
		CredentialHash: strings.Repeat("a", 56), ExpiresAt: time.Now().Add(time.Hour).UTC()}
}

func TestProxySnapshotValidation(t *testing.T) {
	proxy := testProxyConfig()
	proxy.IngressPort = 443
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err != nil {
		t.Fatalf("standard TLS port rejected: %v", err)
	}
	proxy.IngressPort = 24443
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ProxyAccess){
		func(p *ProxyAccess) { p.ID = "" },
		func(p *ProxyAccess) { p.UserID = "" },
		func(p *ProxyAccess) { p.CredentialHash = "invalid" },
		func(p *ProxyAccess) { p.IngressPort = 65536 },
		func(p *ProxyAccess) { p.ExpiresAt = time.Time{} },
	} {
		bad := proxy
		mutate(&bad)
		if _, err := ValidateSnapshot(Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{bad}}); err == nil {
			t.Fatal("invalid proxy accepted")
		}
	}
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy, proxy}}); err == nil {
		t.Fatal("duplicate credential/access accepted")
	}
	second := proxy
	second.ID = "access-2"
	second.CredentialHash = strings.Repeat("b", 56)
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy, second}}); err != nil {
		t.Fatalf("multiple credentials on one TLS port rejected: %v", err)
	}
	rule := Rule{ID: "forward", IngressPort: proxy.IngressPort, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true}
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, Rules: []Rule{rule}, ProxyConfig: []ProxyAccess{proxy}}); err == nil {
		t.Fatal("Trojan and forward shared a TCP port")
	}
	rule.Protocol = "UDP"
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, Rules: []Rule{rule}, ProxyConfig: []ProxyAccess{proxy}}); err != nil {
		t.Fatalf("Trojan TCP and forward UDP conflict: %v", err)
	}
}

func TestValidateSnapshotAcceptsAndChecksProxyLineCandidates(t *testing.T) {
	proxy := testProxyConfig()
	proxy.Candidates = []ProxyLineCandidate{
		{LineID: proxy.LineID, Priority: 10, Weight: 2},
		{LineID: "fallback-line", Priority: 20, Weight: 1},
	}
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ProxyAccess){
		func(value *ProxyAccess) { value.Candidates[1].LineID = value.Candidates[0].LineID },
		func(value *ProxyAccess) { value.Candidates[0].Weight = 0 },
		func(value *ProxyAccess) { value.Candidates[0].LineID = "" },
	} {
		invalid := proxy
		invalid.Candidates = append([]ProxyLineCandidate(nil), proxy.Candidates...)
		mutate(&invalid)
		if _, err := ValidateSnapshot(Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{invalid}}); err == nil {
			t.Fatalf("invalid candidates accepted: %+v", invalid.Candidates)
		}
	}
}

func TestValidateSnapshotAcceptsUnavailableDefaultWithExplicitFallback(t *testing.T) {
	proxy := testProxyConfig()
	proxy.Candidates = []ProxyLineCandidate{{LineID: "fallback-line", Priority: 10, Weight: 1}}
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err != nil {
		t.Fatalf("valid fallback rejected: %v", err)
	}
}
