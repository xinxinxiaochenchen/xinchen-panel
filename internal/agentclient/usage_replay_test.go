package agentclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"github.com/coder/websocket"
)

func TestAgentUsageReplaysAfterDisconnectBeforeApplyingConfig(t *testing.T) {
	outbox, err := NewFileUsageOutbox(filepath.Join(t.TempDir(), "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer outbox.Close()
	batch := sampleUsageBatch()
	if err := outbox.Enqueue(batch); err != nil {
		t.Fatal(err)
	}
	digest, _ := agentproto.UsageBatchDigest(batch)
	_, configDigest, _ := agentproto.CanonicalForwardConfig(nil)
	var runs atomic.Int32
	received := make(chan agentproto.UsageBatch, 2)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		read := func() agentproto.Envelope {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				t.Error(err)
				return agentproto.Envelope{}
			}
			v, err := agentproto.Decode(raw)
			if err != nil {
				t.Error(err)
			}
			return v
		}
		send := func(kind agentproto.MessageType, payload any) {
			body, _ := json.Marshal(payload)
			frame, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: batch.BatchID, NodeID: testNodeID, Type: kind, SentAt: time.Now(), Payload: body})
			if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
				t.Error(err)
			}
		}
		if read().Type != agentproto.TypeHello {
			t.Error("expected hello")
			return
		}
		send(agentproto.TypeConfigSnapshot, agentproto.ConfigSnapshot{Revision: 1, SHA256: configDigest, ValidUntil: time.Now().Add(time.Minute)})
		envelope := read()
		if envelope.Type != agentproto.TypeUsageBatch {
			t.Errorf("config applied before usage replay: %s", envelope.Type)
			return
		}
		got, err := agentproto.DecodeUsageBatch(envelope.Payload)
		if err != nil {
			t.Error(err)
			return
		}
		received <- got
		if runs.Add(1) == 1 {
			return
		} // persisted server-side, but the ACK was lost
		send(agentproto.TypeUsageAck, agentproto.UsageAck{BatchID: batch.BatchID, SHA256: digest})
		if read().Type != agentproto.TypeConfigResult {
			t.Error("config not applied after usage reconciliation")
		}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client, err := New(Config{URL: "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream", NodeID: testNodeID, Version: "1.0", RootCAs: roots, Certificate: testClientCertificate(t), UsageOutbox: outbox}, func() Runtime { return &fakeRuntime{} }, &fakeStateStore{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.RunOnce(ctx)
	if len(outbox.Pending()) != 1 {
		t.Fatal("lost batch without ACK")
	}
	_ = client.RunOnce(ctx)
	if len(outbox.Pending()) != 0 {
		t.Fatal("matching ACK did not remove batch")
	}
	for i := 0; i < 2; i++ {
		select {
		case got := <-received:
			gotDigest, _ := agentproto.UsageBatchDigest(got)
			if gotDigest != digest {
				t.Fatal("replay changed report")
			}
		case <-ctx.Done():
			t.Fatal("usage was not replayed")
		}
	}
}
