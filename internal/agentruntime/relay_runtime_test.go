package agentruntime

import (
	"bytes"
	"context"
	"crypto/tls"
	"strings"
	"testing"

	"controlplane/internal/agentrelay"
)

func relayTestConfig(role RelayRole) RelayConfig {
	secret := bytes.Repeat([]byte{3}, 32)
	if role == RelayEgress {
		return RelayConfig{LineID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423", Generation: 1, Role: role, PreviousNodeID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424", PreviousSecret: secret, PreviousFingerprints: []string{strings.Repeat("a", 64)}}
	}
	if role == RelayIngress {
		return RelayConfig{LineID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423", Generation: 1, Role: role, Next: &RelayNextHop{NodeID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424", Address: "relay.example.com:24443", Port: 24443, Secret: secret, Fingerprints: []string{strings.Repeat("b", 64)}}}
	}
	return RelayConfig{LineID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423", Generation: 1, Role: role, Next: &RelayNextHop{NodeID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424", Address: "relay.example.com:24443", Port: 24443, Secret: secret, Fingerprints: []string{strings.Repeat("b", 64)}}, PreviousNodeID: "prev", PreviousSecret: secret, PreviousFingerprints: []string{strings.Repeat("a", 64)}}
}

func TestRelayReplacementCancelsOldContextAndRetainsReplayWindow(t *testing.T) {
	oldContext, oldCancel := context.WithCancel(context.Background())
	newContext, newCancel := context.WithCancel(context.Background())
	defer newCancel()
	old := &agentrelay.Route{Generation: 7, Context: oldContext, Window: agentrelay.NewReplayWindow(16)}
	fresh := &agentrelay.Route{Generation: 7, Context: newContext, Window: agentrelay.NewReplayWindow(16)}
	endpoint := &relayEndpoint{routes: map[string]*agentrelay.Route{"line": old}, cancels: map[string]context.CancelFunc{"line": oldCancel}}
	endpoint.replace(map[string]*agentrelay.Route{"line": fresh}, map[string]context.CancelFunc{"line": newCancel})
	if oldContext.Err() == nil || newContext.Err() != nil || fresh.Window != old.Window {
		t.Fatal("relay replacement did not revoke old stream and preserve replay protection")
	}
}
func TestBuildRelayRoutesUsesImmutableRouteAndTLSBuilder(t *testing.T) {
	cfg := relayTestConfig(RelayEgress)
	routes, cancels, err := buildRelayRoutes(context.Background(), []RelayConfig{cfg}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || len(cancels) != 1 || routes[cfg.LineID].Window == nil || !routes[cfg.LineID].ExpiresAt.IsZero() {
		t.Fatalf("routes=%v cancels=%v", routes, cancels)
	}
}

func TestBuildRelayRoutesIncludesIngress(t *testing.T) {
	cfg := relayTestConfig(RelayIngress)
	routes, cancels, err := buildRelayRoutes(context.Background(), []RelayConfig{cfg},
		func(context.Context, RelayNextHop) (*tls.Config, error) {
			return &tls.Config{MinVersion: tls.VersionTLS13}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer cancels[cfg.LineID]()
	if route := routes[cfg.LineID]; route == nil || route.Next == nil || route.Next.Address != cfg.Next.Address {
		t.Fatalf("ingress route = %+v", route)
	}
}

func TestRuntimeRejectsRelayWithoutListenerConfiguration(t *testing.T) {
	r := New(Options{})
	defer r.Close()
	if err := r.Apply(context.Background(), Snapshot{Revision: 1, RelayConfig: []RelayConfig{relayTestConfig(RelayEgress)}}); err == nil {
		t.Fatal("relay snapshot accepted without listener TLS")
	}
	if r.Revision() != 0 {
		t.Fatal("failed relay apply advanced revision")
	}
}
