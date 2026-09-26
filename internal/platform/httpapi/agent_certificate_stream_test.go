package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
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
	"github.com/jackc/pgx/v5/pgxpool"
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
	oldPair, renewedPEM, caPEM, keyPEM, roots, _ := rotatingStreamCertificates(t, 3*time.Second)
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
	udpEcho, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udpEcho.Close()
	go func() {
		buffer := make([]byte, 2048)
		for {
			count, address, err := udpEcho.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = udpEcho.WriteTo(buffer[:count], address)
		}
	}()
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	_ = reserved.Close()
	proxyReserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyPort := proxyReserved.Addr().(*net.TCPAddr).Port
	_ = proxyReserved.Close()
	if proxyPort == port {
		t.Fatal("proxy port collides with forward port")
	}
	rule := agentruntime.Rule{ID: "handover-tcp", IngressPort: port, TargetHost: "example.org",
		TargetPort: 443, Protocol: "BOTH", Enabled: true}
	proxy := agentruntime.ProxyAccess{ID: "handover-proxy", UserID: "handover-user", LineID: "handover-line",
		IngressPort: proxyPort, CredentialHash: strings.Repeat("a", 56), ExpiresAt: time.Now().Add(time.Hour)}
	_, digest, err := agentproto.CanonicalConfig([]agentruntime.Rule{rule}, []agentruntime.ProxyAccess{proxy})
	if err != nil {
		t.Fatal(err)
	}
	store := &streamStore{desired: orchestration.DesiredRevision{NodeID: certificateTestNodeID, Revision: 1,
		Digest: digest, Snapshot: agentruntime.Snapshot{Revision: 1, Rules: []agentruntime.Rule{rule},
			ProxyConfig: []agentruntime.ProxyAccess{proxy}}},
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
		ProxyTLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: server.TLS.Certificates},
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}, DialTCP: func(context.Context, string) (net.Conn, error) {
			return net.Dial("tcp", echo.Addr().String())
		}, DialUDP: func(context.Context, string) (net.Conn, error) {
			return net.Dial("udp", udpEcho.LocalAddr().String())
		}})
	client, err := agentclient.New(agentclient.Config{URL: "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream",
		NodeID: certificateTestNodeID, Version: "v1", RootCAs: serverRoots, Certificate: oldPair,
		CertFile: certPath, KeyFile: keyPath, HeartbeatInterval: 100 * time.Millisecond, ProxyReady: true},
		func() agentclient.Runtime { return runtime }, handoverState{})
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
	udp, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	checkUDP := func(value string) {
		t.Helper()
		if err := udp.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := udp.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(value))
		if _, err := io.ReadFull(udp, got); err != nil || string(got) != value {
			t.Fatalf("UDP session response = %q, %v", got, err)
		}
	}
	checkUDP("before")
	proxyClient, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)),
		&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: serverRoots})
	if err != nil {
		t.Fatal(err)
	}
	defer proxyClient.Close()
	proxyRequest := append([]byte(proxy.CredentialHash+"\r\n"), 1, 3, byte(len("example.org")))
	proxyRequest = append(proxyRequest, "example.org"...)
	proxyRequest = binary.BigEndian.AppendUint16(proxyRequest, 443)
	proxyRequest = append(proxyRequest, '\r', '\n')
	checkProxy := func(value string) {
		t.Helper()
		if err := proxyClient.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		payload := []byte(value)
		if proxyRequest != nil {
			payload = append(proxyRequest, payload...)
			proxyRequest = nil
		}
		if _, err := proxyClient.Write(payload); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(value))
		if _, err := io.ReadFull(proxyClient, got); err != nil || string(got) != value {
			t.Fatalf("Trojan session response = %q, %v", got, err)
		}
	}
	checkProxy("before")
	oldCert, _ := x509.ParseCertificate(oldPair.Certificate[0])
	if delay := time.Until(oldCert.NotAfter.Add(350 * time.Millisecond)); delay > 0 {
		time.Sleep(delay)
	}
	checkEcho("after")
	checkUDP("after")
	checkProxy("after")
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

func rotatingStreamCertificates(t *testing.T, oldLifetime time.Duration) (tls.Certificate, []byte, []byte, []byte, *x509.CertPool, []byte) {
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
	oldPEM := issue(2, now.Add(oldLifetime))
	newPEM := issue(3, now.Add(90*time.Minute))
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(oldPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	caKeyDER, err := x509.MarshalPKCS8PrivateKey(caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	return pair, newPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), roots,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caKeyDER})
}

func TestAgentStreamRemainsAuthorizedAfterOriginalCertificateExpires(t *testing.T) {
	_, digest, err := agentproto.CanonicalForwardConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &streamStore{desired: orchestration.DesiredRevision{NodeID: certificateTestNodeID, Revision: 1,
		Digest: digest, Snapshot: agentruntime.Snapshot{Revision: 1}},
		results: make(chan agentproto.ConfigResult, 1), heartbeats: make(chan agentproto.Heartbeat, 1)}
	pair, renewedPEM, _, _, roots, _ := rotatingStreamCertificates(t, 3*time.Second)
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

func TestAgentStreamCertificateUpdateChecksDatabaseGrantsAndNodeState(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" && os.Getenv("CONTROL_TEST_DB_NAME") != "" && os.Getenv("POSTGRES_PASSWORD") != "" {
		databaseURL = (&url.URL{Scheme: "postgres", User: url.UserPassword("controlplane", os.Getenv("POSTGRES_PASSWORD")),
			Host: "127.0.0.1:5432", Path: "/" + os.Getenv("CONTROL_TEST_DB_NAME")}).String()
	}
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	oldPair, renewedPEM, caPEM, _, _, caKeyPEM := rotatingStreamCertificates(t, time.Hour)
	issuer, err := agentidentity.NewIssuer(caPEM, caKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	service := agentidentity.NewEnrollmentService(pool, issuer)
	stream := NewAgentStreamHandler(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), service, nil, nil)
	oldCert, err := x509.ParseCertificate(oldPair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	newBlock, _ := pem.Decode(renewedPEM)
	newCert, err := x509.ParseCertificate(newBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	oldFingerprint := sha256.Sum256(oldCert.Raw)
	newFingerprint := sha256.Sum256(newCert.Raw)
	var groupID string
	if err := pool.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region)
VALUES(gen_random_uuid(),gen_random_uuid()::text,'Handover test','US') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE id=$1`, groupID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO nodes(id,group_id,name,region,host,capabilities)
VALUES($1,$2,'Handover test node','US','handover.example.org',ARRAY['forward'])`, certificateTestNodeID, groupID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id=$1`, certificateTestNodeID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO agents(id,node_id,cert_fingerprint,cert_expires_at,status)
VALUES(gen_random_uuid(),$1,$2,$3,'online')`, certificateTestNodeID, hex.EncodeToString(oldFingerprint[:]), oldCert.NotAfter); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM agents WHERE node_id=$1`, certificateTestNodeID)
	})
	message, err := json.Marshal(agentproto.CertificateUpdate{CertificatePEM: string(renewedPEM)})
	if err != nil {
		t.Fatal(err)
	}
	accept := func() error {
		_, _, err := stream.acceptCertificateUpdate(ctx, certificateTestNodeID, oldCert, oldCert, message)
		return err
	}
	if err := accept(); !errors.Is(err, agentidentity.ErrAgentUnauthorized) {
		t.Fatalf("ungranted renewal accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agent_certificate_grants(node_id,fingerprint,parent_fingerprint,certificate_pem,expires_at)
VALUES($1,$2,$3,$4,$5)`, certificateTestNodeID, hex.EncodeToString(newFingerprint[:]),
		hex.EncodeToString(oldFingerprint[:]), renewedPEM, newCert.NotAfter); err != nil {
		t.Fatal(err)
	}
	if err := accept(); err != nil {
		t.Fatalf("granted renewal rejected: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agents SET status='revoked' WHERE node_id=$1`, certificateTestNodeID); err != nil {
		t.Fatal(err)
	}
	if err := accept(); !errors.Is(err, agentidentity.ErrAgentUnauthorized) {
		t.Fatalf("revoked Agent accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agents SET status='online' WHERE node_id=$1`, certificateTestNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=false WHERE id=$1`, certificateTestNodeID); err != nil {
		t.Fatal(err)
	}
	if err := accept(); !errors.Is(err, agentidentity.ErrAgentUnauthorized) {
		t.Fatalf("disabled node accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=true WHERE id=$1`, certificateTestNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM agent_certificate_grants WHERE node_id=$1`, certificateTestNodeID); err != nil {
		t.Fatal(err)
	}
	if err := accept(); !errors.Is(err, agentidentity.ErrAgentUnauthorized) {
		t.Fatalf("deleted renewal grant accepted: %v", err)
	}
}
