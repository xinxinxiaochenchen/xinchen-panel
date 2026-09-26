package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"controlplane/internal/agentclient"
	"controlplane/internal/agentidentity"
	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
	"controlplane/internal/orchestration"
	"controlplane/internal/platform/id"
	"github.com/coder/websocket"
)

type streamAuthenticator struct {
	mu      sync.Mutex
	revoked bool
	roots   *x509.CertPool
}

func (s *streamAuthenticator) AuthenticateCertificate(_ context.Context, cert *x509.Certificate) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked || cert.Subject.CommonName != certificateTestNodeID || !time.Now().Before(cert.NotAfter) {
		return "", agentidentity.ErrAgentUnauthorized
	}
	if s.roots != nil {
		if _, err := cert.Verify(x509.VerifyOptions{Roots: s.roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
			return "", agentidentity.ErrAgentUnauthorized
		}
	}
	return certificateTestNodeID, nil
}

type streamStore struct {
	mu         sync.Mutex
	desired    orchestration.DesiredRevision
	results    chan agentproto.ConfigResult
	heartbeats chan agentproto.Heartbeat
	online     int
}

func (s *streamStore) Desired(context.Context, string) (orchestration.DesiredRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.desired, nil
}
func (s *streamStore) RecordResult(_ context.Context, _ string, revision int64, digest, status, code, message string) error {
	s.results <- agentproto.ConfigResult{Revision: revision, SHA256: digest, Status: status, ErrorCode: code, ErrorMessage: message}
	return nil
}
func (s *streamStore) MarkOnline(context.Context, string, string, []string) error {
	s.mu.Lock()
	s.online++
	s.mu.Unlock()
	return nil
}
func (s *streamStore) RecordHeartbeat(_ context.Context, _ string, value agentproto.Heartbeat) error {
	s.heartbeats <- value
	return nil
}
func (s *streamStore) MarkOffline(context.Context, string) error { return nil }

func testStreamCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Stream CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	uri, _ := url.Parse("spiffe://network-control-plane/agent/" + certificateTestNodeID)
	clientPublic, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: certificateTestNodeID},
		URIs: []*url.URL{uri}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	clientDER, err := x509.CreateCertificate(rand.Reader, client, ca, clientPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	pool.AddCert(caCert)
	return pair, pool
}

func TestAgentStreamRequiresMTLSAndDeliversSnapshotWithResult(t *testing.T) {
	_, digest, err := agentproto.CanonicalForwardConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &streamStore{desired: orchestration.DesiredRevision{NodeID: certificateTestNodeID, Revision: 1,
		Digest: digest, Snapshot: agentruntime.Snapshot{Revision: 1}},
		results: make(chan agentproto.ConfigResult, 1), heartbeats: make(chan agentproto.Heartbeat, 1)}
	auth := &streamAuthenticator{}
	stream := NewAgentStreamHandler(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), auth, store, store)
	stream.pollEvery = 10 * time.Millisecond
	stream.renewEvery = 40 * time.Millisecond
	server := httptest.NewUnstartedServer(NewAgentHandlerWithStream(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, stream))
	pair, roots := testStreamCertificate(t)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	endpoint := "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: server.Client()}); err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("uncredentialed stream = %v, %+v", err, response)
	}
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{pair}
	client.Transport = transport
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	helloPayload, _ := json.Marshal(agentproto.Hello{AgentVersion: "1.0.0", AppliedRevision: 1, Capabilities: []string{"forward"}})
	hello, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424",
		NodeID: certificateTestNodeID, Type: agentproto.TypeHello, SentAt: time.Now(), Payload: helloPayload})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	message, err := agentproto.Decode(raw)
	if err != nil || message.Type != agentproto.TypeConfigSnapshot || message.NodeID != certificateTestNodeID {
		t.Fatalf("snapshot message = %+v, %v", message, err)
	}
	snapshot, err := agentproto.DecodeConfigSnapshot(message.Payload, time.Now())
	if err != nil || snapshot.Revision != 1 || snapshot.SHA256 != digest {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
	resultPayload, _ := json.Marshal(agentproto.ConfigResult{Revision: 1, SHA256: digest, Status: "applied"})
	result, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e425",
		NodeID: certificateTestNodeID, Type: agentproto.TypeConfigResult, SentAt: time.Now(), Payload: resultPayload})
	if err := conn.Write(ctx, websocket.MessageText, result); err != nil {
		t.Fatal(err)
	}
	select {
	case saved := <-store.results:
		if saved.Status != "applied" || saved.Revision != 1 {
			t.Fatalf("saved result = %+v", saved)
		}
	case <-ctx.Done():
		t.Fatal("result not saved")
	}
	heartbeatPayload, _ := json.Marshal(agentproto.Heartbeat{UptimeSeconds: 10, CPUPct: 2, EngineStatus: "running"})
	heartbeat, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e426",
		NodeID: certificateTestNodeID, Type: agentproto.TypeHeartbeat, SentAt: time.Now(), Payload: heartbeatPayload})
	if err := conn.Write(ctx, websocket.MessageText, heartbeat); err != nil {
		t.Fatal(err)
	}
	select {
	case saved := <-store.heartbeats:
		if saved.UptimeSeconds != 10 {
			t.Fatalf("saved heartbeat = %+v", saved)
		}
	case <-ctx.Done():
		t.Fatal("heartbeat not saved")
	}
	_, renewalRaw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	renewalMessage, err := agentproto.Decode(renewalRaw)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := agentproto.DecodeConfigSnapshot(renewalMessage.Payload, time.Now())
	if err != nil || renewed.Revision != snapshot.Revision || renewed.SHA256 != snapshot.SHA256 || !renewed.ValidUntil.After(snapshot.ValidUntil) {
		t.Fatalf("configuration lease was not renewed: %+v, %v", renewed, err)
	}
}

func TestAgentStreamRejectsNodeSpoofAndRevokedCertificate(t *testing.T) {
	store := &streamStore{results: make(chan agentproto.ConfigResult, 1), heartbeats: make(chan agentproto.Heartbeat, 1)}
	auth := &streamAuthenticator{}
	stream := NewAgentStreamHandler(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), auth, store, store)
	server := httptest.NewUnstartedServer(NewAgentHandlerWithStream(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, stream))
	pair, roots := testStreamCertificate(t)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{pair}
	client.Transport = transport
	endpoint := "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	helloPayload, _ := json.Marshal(agentproto.Hello{AgentVersion: "1.0.0", AppliedRevision: 0, Capabilities: []string{"forward"}})
	spoofed := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e999"
	frame, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e427",
		NodeID: spoofed, Type: agentproto.TypeHello, SentAt: time.Now(), Payload: helloPayload})
	if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("spoofed node stayed connected")
	}
	conn.CloseNow()
	auth.mu.Lock()
	auth.revoked = true
	auth.mu.Unlock()
	if _, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: client}); err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked certificate connected: %v, %+v", err, response)
	}
}

func TestAgentStreamClosesAfterCertificateRevocation(t *testing.T) {
	store := &streamStore{results: make(chan agentproto.ConfigResult, 1), heartbeats: make(chan agentproto.Heartbeat, 1)}
	auth := &streamAuthenticator{}
	stream := NewAgentStreamHandler(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), auth, store, store)
	stream.recheckEvery = 20 * time.Millisecond
	server := httptest.NewUnstartedServer(NewAgentHandlerWithStream(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, stream))
	pair, roots := testStreamCertificate(t)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{pair}
	client.Transport = transport
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(server.URL, "https")+"/api/v1/agent/stream",
		&websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	payload, _ := json.Marshal(agentproto.Hello{AgentVersion: "1.0.0", AppliedRevision: 0, Capabilities: []string{"forward"}})
	messageID, _ := id.NewV7()
	frame, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: messageID,
		NodeID: certificateTestNodeID, Type: agentproto.TypeHello, SentAt: time.Now(), Payload: payload})
	if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		store.mu.Lock()
		online := store.online
		store.mu.Unlock()
		if online > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hello was not accepted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	auth.mu.Lock()
	auth.revoked = true
	auth.mu.Unlock()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("revoked live Agent stream remained open")
	}
	if ctx.Err() != nil {
		t.Fatalf("revocation did not terminate before deadline: %v", ctx.Err())
	}
}

func TestAgentClientAndControlPlaneStreamApplyEndToEnd(t *testing.T) {
	portReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyPort := portReservation.Addr().(*net.TCPAddr).Port
	portReservation.Close()
	proxyConfig := []agentruntime.ProxyAccess{{ID: "access", UserID: "user", LineID: "line", IngressPort: proxyPort,
		CredentialHash: strings.Repeat("a", 56), ExpiresAt: time.Now().Add(time.Hour)}}
	_, digest, err := agentproto.CanonicalConfig(nil, proxyConfig)
	if err != nil {
		t.Fatal(err)
	}
	store := &streamStore{desired: orchestration.DesiredRevision{NodeID: certificateTestNodeID, Revision: 1,
		Digest: digest, Snapshot: agentruntime.Snapshot{Revision: 1, ProxyConfig: proxyConfig}, Status: "pending"},
		results: make(chan agentproto.ConfigResult, 1), heartbeats: make(chan agentproto.Heartbeat, 1)}
	stream := NewAgentStreamHandler(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), &streamAuthenticator{}, store, store)
	stream.pollEvery = 10 * time.Millisecond
	server := httptest.NewUnstartedServer(NewAgentHandlerWithStream(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, stream))
	pair, clientCA := testStreamCertificate(t)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: clientCA}
	server.StartTLS()
	defer server.Close()
	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(server.Certificate())
	state, err := agentclient.NewFileStateStore(filepath.Join(t.TempDir(), "applied.json"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := agentclient.New(agentclient.Config{URL: "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream",
		NodeID: certificateTestNodeID, Version: "1.0.0", RootCAs: serverRoots, Certificate: pair, ProxyReady: true},
		func() agentclient.Runtime {
			return agentruntime.New(agentruntime.Options{BindHost: "127.0.0.1",
				ProxyTLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: server.TLS.Certificates},
				Resolve: func(context.Context, string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
				},
				DialTCP: func(context.Context, string) (net.Conn, error) {
					client, peer := net.Pipe()
					go func() { defer peer.Close(); _, _ = io.Copy(peer, peer) }()
					return client, nil
				},
			})
		}, state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	done := make(chan error, 1)
	go func() { done <- client.RunOnce(ctx) }()
	select {
	case result := <-store.results:
		if result.Revision != 1 || result.Status != "applied" || result.SHA256 != digest {
			t.Fatalf("Agent result = %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("Agent did not apply control plane snapshot")
	}
	proxyClient, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)),
		&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: serverRoots, ServerName: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer proxyClient.Close()
	proxyClient.SetDeadline(time.Now().Add(2 * time.Second))
	request := append([]byte(strings.Repeat("a", 56)+"\r\n"), 1, 3, 11)
	request = append(request, []byte("example.org")...)
	request = binary.BigEndian.AppendUint16(request, 443)
	request = append(request, []byte("\r\nhello")...)
	if _, err := proxyClient.Write(request); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 5)
	if _, err := io.ReadFull(proxyClient, response); err != nil || string(response) != "hello" {
		t.Fatalf("end-to-end Trojan response=%q, %v", response, err)
	}
	_, revokedDigest, err := agentproto.CanonicalForwardConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.desired = orchestration.DesiredRevision{NodeID: certificateTestNodeID, Revision: 2, Digest: revokedDigest,
		Snapshot: agentruntime.Snapshot{Revision: 2}, Status: "pending"}
	store.mu.Unlock()
	select {
	case result := <-store.results:
		if result.Revision != 2 || result.Status != "applied" {
			t.Fatalf("revocation ACK=%+v", result)
		}
	case <-ctx.Done():
		t.Fatal("revocation not applied")
	}
	if _, err := proxyClient.Read(response); err == nil {
		t.Fatal("Agent retained revoked proxy connection")
	}
	cancel()
	<-done
	loaded, err := state.Load()
	if err != nil || loaded.Revision != 2 {
		t.Fatalf("saved Agent state = %+v, %v", loaded, err)
	}
}
