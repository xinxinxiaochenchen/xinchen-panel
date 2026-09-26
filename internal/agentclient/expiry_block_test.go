package agentclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"github.com/coder/websocket"
)

type blockingStateStore struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingStateStore) Save(agentproto.ConfigSnapshot) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return nil
}

func TestAgentClosesRuntimeAtLeaseExpiryEvenWhenStateSaveBlocks(t *testing.T) {
	_, digest, _ := agentproto.CanonicalForwardConfig(nil)
	saved := &blockingStateStore{entered: make(chan struct{}), release: make(chan struct{})}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, _, err := conn.Read(ctx); err != nil {
			t.Error(err)
			return
		}
		payload, _ := json.Marshal(agentproto.ConfigSnapshot{Revision: 1, SHA256: digest, ValidUntil: time.Now().Add(250 * time.Millisecond)})
		frame, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", NodeID: testNodeID, Type: agentproto.TypeConfigSnapshot, SentAt: time.Now(), Payload: payload})
		_ = conn.Write(ctx, websocket.MessageText, frame)
		<-ctx.Done()
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	runtime := &fakeRuntime{}
	client, err := New(Config{URL: "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream", NodeID: testNodeID, Version: "1.0", RootCAs: roots, Certificate: testClientCertificate(t)}, func() Runtime { return runtime }, saved)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.RunOnce(ctx) }()
	select {
	case <-saved.entered:
	case <-ctx.Done():
		t.Fatal("snapshot was not saved")
	}
	time.Sleep(350 * time.Millisecond)
	runtime.mu.Lock()
	closed := runtime.closed
	runtime.mu.Unlock()
	close(saved.release)
	<-done
	if !closed {
		t.Fatal("runtime kept serving after configuration lease expired while Save blocked")
	}
}
