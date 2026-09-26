package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentclient"
	"controlplane/internal/agentidentity"
	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
	"controlplane/internal/orchestration"
	"github.com/coder/websocket"
)

type handoverEnrollment struct {
	renewedPEM []byte
	caPEM      []byte
}

func (h handoverEnrollment) Enroll(context.Context, string, []byte, string) (agentidentity.EnrollmentResult, error) {
	return agentidentity.EnrollmentResult{}, agentidentity.ErrInvalidEnrollment
}

func (h handoverEnrollment) Renew(_ context.Context, _ *x509.Certificate, _ []byte, _ string) (agentidentity.EnrollmentResult, error) {
	block, _ := pem.Decode(h.renewedPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return agentidentity.EnrollmentResult{}, err
	}
	return agentidentity.EnrollmentResult{NodeID: certificateTestNodeID, CertificatePEM: h.renewedPEM,
		CACertificatePEM: h.caPEM, ExpiresAt: cert.NotAfter}, nil
}

type handoverState struct{}

func (handoverState) Save(agentproto.ConfigSnapshot) error { return nil }

func TestAgentRuntimeSurvivesOriginalCertificateExpiryAfterHandover(t *testing.T) {
	oldPair, renewedPEM, caPEM, keyPEM, roots := rotatingStreamCertificates(t)
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			connection, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer connection.Close(); _, _ = io.Copy(connection, connection) }()
		}
	}()
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	_ = reserved.Close()
	rule := agentruntime.Rule{ID: "handover-tcp", IngressPort: port, TargetHost: "example.org",
		TargetPort: 443, Protocol: "TCP", Enabled: true}
	_, digest, err := agentproto.CanonicalForwardConfig([]agentruntime.Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	store := &streamStore{desired: orchestration.DesiredRevision{NodeID: certificateTestNodeID, Revision: 1,
		Digest: digest, Snapshot: agentruntime.Snapshot{Revision: 1, Rules: []agentruntime.Rule{rule}}},
		results: make(chan agentproto.ConfigResult, 100), heartbeats: make(chan agentproto.Heartbeat, 100)}
	auth := &streamAuthenticator{roots: roots}
	stream := NewAgentStreamHandler(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), auth, store, store)
	stream.recheckEvery = 25 * time.Millisecond
	stream.pollEvery = 100 * time.Millisecond
	stream.renewEvery = 100 * time.Millisecond
	server := httptest.NewUnstartedServer(NewAgentHandlerWithStream(slog.New(slog.NewTextHandler(io.Discard, nil)),
		handoverEnrollment{renewedPEM: renewedPEM, caPEM: caPEM}, stream))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(server.Certificate())
	serverRoots.AppendCertsFromPEM(caPEM)
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: oldPair.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	runtime := agentruntime.New(agentruntime.Options{BindHost: "127.0.0.1",
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}, DialTCP: func(context.Context, string) (net.Conn, error) {
			return net.Dial("tcp", echo.Addr().String())
		}})
	client, err := agentclient.New(agentclient.Config{URL: "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream",
		NodeID: certificateTestNodeID, Version: "v1", RootCAs: serverRoots, Certificate: oldPair,
		CertFile: certPath, KeyFile: keyPath, HeartbeatInterval: 100 * time.Millisecond}, func() agentclient.Runtime { return runtime }, handoverState{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	select {
	case result := <-store.results:
		if result.Status != "applied" {
			t.Fatalf("apply: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("snapshot not applied")
	}
	data, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	checkEcho := func(value string) {
		t.Helper()
		if err := data.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := data.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(value))
		if _, err := io.ReadFull(data, got); err != nil || string(got) != value {
			t.Fatalf("TCP session response = %q, %v", got, err)
		}
	}
	checkEcho("before")
	oldCert, _ := x509.ParseCertificate(oldPair.Certificate[0])
	if delay := time.Until(oldCert.NotAfter.Add(350 * time.Millisecond)); delay > 0 {
		time.Sleep(delay)
	}
	checkEcho("after")
	select {
	case err := <-done:
		t.Fatalf("Agent stopped after renewal: %v", err)
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Agent did not stop after cancellation")
	}
}

func rotatingStreamCertificates(t *testing.T) (tls.Certificate, []byte, []byte, []byte, *x509.CertPool) {
	t.Helper()
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Rotation CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(2 * time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := url.Parse("spiffe://network-control-plane/agent/" + certificateTestNodeID)
	issue := func(serial int64, expires time.Time) []byte {
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: certificateTestNodeID},
			URIs: []*url.URL{identity}, NotBefore: now.Add(-time.Minute), NotAfter: expires,
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, public, caPrivate)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	oldPEM := issue(2, now.Add(3*time.Second))
	newPEM := issue(3, now.Add(time.Hour))
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(oldPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return pair, newPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), roots
}

func TestAgentStreamRemainsAuthorizedAfterOriginalCertificateExpires(t *testing.T) {
	_, digest, err := agentproto.CanonicalForwardConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &streamStore{desired: orchestration.DesiredRevision{NodeID: certificateTestNodeID, Revision: 1,
		Digest: digest, Snapshot: agentruntime.Snapshot{Revision: 1}},
		results: make(chan agentproto.ConfigResult, 1), heartbeats: make(chan agentproto.Heartbeat, 1)}
	pair, renewedPEM, _, _, roots := rotatingStreamCertificates(t)
	auth := &streamAuthenticator{roots: roots}
	stream := NewAgentStreamHandler(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), auth, store, store)
	stream.recheckEvery = 25 * time.Millisecond
	stream.pollEvery = 5 * time.Second
	server := httptest.NewUnstartedServer(NewAgentHandlerWithStream(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, stream))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{pair}
	client.Transport = transport
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(server.URL, "https")+"/api/v1/agent/stream", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	send := func(kind agentproto.MessageType, payload any) {
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e425",
			NodeID: certificateTestNodeID, Type: kind, SentAt: time.Now(), Payload: body})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
	}
	send(agentproto.TypeHello, agentproto.Hello{AgentVersion: "v1", Capabilities: []string{"forward"}})
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	} // snapshot
	send(agentproto.TypeCertificateUpdate, agentproto.CertificateUpdate{CertificatePEM: string(renewedPEM)})
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	message, err := agentproto.Decode(raw)
	if err != nil || message.Type != agentproto.TypeCertificateUpdateAck {
		t.Fatalf("certificate ack: %+v %v", message, err)
	}
	old, _ := x509.ParseCertificate(pair.Certificate[0])
	if delay := time.Until(old.NotAfter.Add(250 * time.Millisecond)); delay > 0 {
		time.Sleep(delay)
	}
	send(agentproto.TypeHeartbeat, agentproto.Heartbeat{UptimeSeconds: 3, EngineStatus: "running"})
	select {
	case <-store.heartbeats:
	case <-ctx.Done():
		t.Fatal("control stream stopped after old certificate expired")
	}
}

func TestAgentStreamCertificateUpdateRequiresSameKeyAndCurrentAuthorization(t *testing.T) {
	pair, _ := testStreamCertificate(t)
	certificate := pair.Leaf
	if certificate == nil {
		certificate, _ = x509.ParseCertificate(pair.Certificate[0])
	}
	auth := &streamAuthenticator{}
	stream := NewAgentStreamHandler(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), auth, nil, nil)
	body, _ := json.Marshal(agentproto.CertificateUpdate{CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]}))})
	next, digest, err := stream.acceptCertificateUpdate(context.Background(), certificateTestNodeID, certificate, certificate, body)
	if err != nil || next == nil || digest == "" {
		t.Fatalf("same certificate retry: %v", err)
	}
	otherPair, _ := testStreamCertificate(t)
	otherBody, _ := json.Marshal(agentproto.CertificateUpdate{CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherPair.Certificate[0]}))})
	if _, _, err := stream.acceptCertificateUpdate(context.Background(), certificateTestNodeID, certificate, certificate, otherBody); err == nil {
		t.Fatal("different key accepted")
	}
	auth.mu.Lock()
	auth.revoked = true
	auth.mu.Unlock()
	if _, _, err := stream.acceptCertificateUpdate(context.Background(), certificateTestNodeID, certificate, certificate, body); err == nil {
		t.Fatal("revoked certificate accepted")
	}
}
