package agentclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"github.com/coder/websocket"
)

func TestAgentClosesRuntimeWhenSnapshotLeaseExpires(t *testing.T) {
	_, digest, err := agentproto.CanonicalForwardConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
		payload, _ := json.Marshal(agentproto.ConfigSnapshot{Revision: 1, SHA256: digest, ValidUntil: time.Now().Add(100 * time.Millisecond)})
		frame, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424", NodeID: testNodeID,
			Type: agentproto.TypeConfigSnapshot, SentAt: time.Now(), Payload: payload})
		if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
			return
		}
		_, _, _ = conn.Read(ctx) // ACK
		_, _, _ = conn.Read(ctx) // Keep the stream open without a lease renewal.
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	runtime := &fakeRuntime{}
	client, err := New(Config{URL: "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream", NodeID: testNodeID,
		Version: "1.0.0", RootCAs: roots, Certificate: testClientCertificate(t)}, func() Runtime { return runtime }, &fakeStateStore{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	if err := client.RunOnce(ctx); err == nil {
		t.Fatal("expired snapshot kept stream open")
	}
	if time.Since(started) > time.Second {
		t.Fatal("runtime stayed active beyond snapshot lease")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if !runtime.closed || len(runtime.snapshots) != 1 {
		t.Fatal("expired lease did not close applied runtime")
	}
}
