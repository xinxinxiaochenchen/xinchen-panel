package agentclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
	"github.com/coder/websocket"
)

const testNodeID = "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"

type fakeRuntime struct {
	mu        sync.Mutex
	snapshots []agentruntime.Snapshot
	applyErr  error
	closed    bool
}

func (f *fakeRuntime) Apply(_ context.Context, snapshot agentruntime.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applyErr != nil {
		return f.applyErr
	}
	f.snapshots = append(f.snapshots, snapshot)
	return nil
}
func (f *fakeRuntime) Close() error              { f.mu.Lock(); defer f.mu.Unlock(); f.closed = true; return nil }
func (f *fakeRuntime) Stats() agentruntime.Stats { return agentruntime.Stats{} }

type fakeStateStore struct {
	mu    sync.Mutex
	last  agentproto.ConfigSnapshot
	saves int
}

func (s *fakeStateStore) Save(value agentproto.ConfigSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = value
	s.saves++
	return nil
}

func testClientCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: testNodeID},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func TestAgentClientAppliesSnapshotAndReportsResult(t *testing.T) {
	_, digest, err := agentproto.CanonicalForwardConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan agentproto.ConfigResult, 1)
	helloRevision := make(chan int64, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
			t.Error("client certificate missing")
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		envelope, err := agentproto.Decode(raw)
		if err != nil || envelope.Type != agentproto.TypeHello || envelope.NodeID != testNodeID {
			t.Errorf("hello = %+v, %v", envelope, err)
			return
		}
		hello, err := agentproto.DecodeHello(envelope.Payload)
		if err != nil {
			t.Error(err)
			return
		}
		helloRevision <- hello.AppliedRevision
		payload, _ := json.Marshal(agentproto.ConfigSnapshot{Revision: 1, SHA256: digest, ValidUntil: time.Now().Add(time.Minute), ForwardConfig: []agentruntime.Rule{}})
		frame, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424", NodeID: testNodeID,
			Type: agentproto.TypeConfigSnapshot, SentAt: time.Now(), Payload: payload})
		if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Error(err)
			return
		}
		_, raw, err = conn.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		envelope, err = agentproto.Decode(raw)
		if err != nil || envelope.Type != agentproto.TypeConfigResult {
			t.Errorf("result envelope = %+v, %v", envelope, err)
			return
		}
		saved, err := agentproto.DecodeConfigResult(envelope.Payload)
		if err != nil {
			t.Error(err)
			return
		}
		result <- saved
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	runtime := &fakeRuntime{}
	state := &fakeStateStore{}
	client, err := New(Config{URL: "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream",
		NodeID: testNodeID, Version: "1.0.0", RootCAs: roots, Certificate: testClientCertificate(t)},
		func() Runtime { return runtime }, state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.RunOnce(ctx)
	select {
	case revision := <-helloRevision:
		if revision != 0 {
			t.Fatalf("fresh runtime claimed revision %d", revision)
		}
	default:
		t.Fatal("hello not received")
	}
	select {
	case saved := <-result:
		if saved.Status != "applied" || saved.Revision != 1 || saved.SHA256 != digest {
			t.Fatalf("result = %+v", saved)
		}
	default:
		t.Fatal("result not received")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.snapshots) != 1 || runtime.snapshots[0].Revision != 1 || !runtime.closed {
		t.Fatalf("runtime = %+v", runtime)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.saves != 1 || state.last.Revision != 1 {
		t.Fatalf("state = %+v", state)
	}
}

func TestAgentClientReconnectsWithFreshRuntime(t *testing.T) {
	connections := make(chan struct{}, 8)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		message, err := agentproto.Decode(raw)
		if err != nil {
			t.Error(err)
			return
		}
		hello, err := agentproto.DecodeHello(message.Payload)
		if err != nil || hello.AppliedRevision != 0 {
			t.Errorf("reconnect hello = %+v, %v", hello, err)
			return
		}
		connections <- struct{}{}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	var mu sync.Mutex
	var runtimes []*fakeRuntime
	client, err := New(Config{URL: "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream",
		NodeID: testNodeID, Version: "1.0.0", RootCAs: roots, Certificate: testClientCertificate(t),
		ReconnectMin: 10 * time.Millisecond, ReconnectMax: 20 * time.Millisecond},
		func() Runtime {
			value := &fakeRuntime{}
			mu.Lock()
			runtimes = append(runtimes, value)
			mu.Unlock()
			return value
		}, &fakeStateStore{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	for range 2 {
		select {
		case <-connections:
		case <-ctx.Done():
			t.Fatal("Agent did not reconnect")
		}
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(runtimes) < 2 {
		t.Fatalf("runtimes created = %d", len(runtimes))
	}
	for _, runtime := range runtimes {
		runtime.mu.Lock()
		closed := runtime.closed
		runtime.mu.Unlock()
		if !closed {
			t.Fatal("disconnected runtime kept forwarding")
		}
	}
}
