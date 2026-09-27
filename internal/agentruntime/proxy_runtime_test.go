package agentruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func proxyTestTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute),
		NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, public, private)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}},
		&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "127.0.0.1"}
}

func trojanRequest(hash, host string, port int, command byte) []byte {
	request := append([]byte(hash+"\r\n"), command, 3, byte(len(host)))
	request = append(request, host...)
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	return append(request, '\r', '\n')
}

func TestTrojanTLSRelaysAndRevokesCredentials(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	serverTLS, clientTLS := proxyTestTLS(t)
	var dials atomic.Int64
	runtime := New(Options{BindHost: "127.0.0.1", ProxyTLSConfig: serverTLS,
		Resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
			if host == "private.example" {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}, DialTCP: func(_ context.Context, address string) (net.Conn, error) {
			dials.Add(1)
			if address != "8.8.8.8:443" {
				t.Errorf("unexpected target %q", address)
			}
			return net.Dial("tcp", echo.Addr().String())
		}})
	defer runtime.Close()
	proxy := testProxyConfig()
	proxy.IngressPort = unusedTCPPort(t)
	other := proxy
	other.ID = "other"
	other.CredentialHash = strings.Repeat("b", 56)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy, other}}); err != nil {
		t.Fatal(err)
	}
	connect := func(hash, host string, command byte) *tls.Conn {
		t.Helper()
		conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxy.IngressPort)), clientTLS)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write(append(trojanRequest(hash, host, 443, command), []byte("hello")...)); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	client := connect(proxy.CredentialHash, "example.org", 1)
	defer client.Close()
	response := make([]byte, 5)
	if _, err := io.ReadFull(client, response); err != nil || string(response) != "hello" {
		t.Fatalf("Trojan response = %q, %v", response, err)
	}
	for _, denied := range []struct {
		hash, host string
		command    byte
	}{
		{strings.Repeat("d", 56), "example.org", 1}, {proxy.CredentialHash, "private.example", 1}, {proxy.CredentialHash, "example.org", 3},
	} {
		conn := connect(denied.hash, denied.host, denied.command)
		if n, err := conn.Read(response); err == nil || n != 0 {
			t.Fatalf("denied request returned %d bytes, %v", n, err)
		}
		conn.Close()
	}
	if dials.Load() != 1 {
		t.Fatalf("unauthorized target dialed: %d", dials.Load())
	}
	otherClient := connect(other.CredentialHash, "example.org", 1)
	defer otherClient.Close()
	if _, err := io.ReadFull(otherClient, response); err != nil {
		t.Fatal(err)
	}
	proxy.CredentialHash = strings.Repeat("c", 56)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 2, ProxyConfig: []ProxyAccess{proxy, other}}); err != nil {
		t.Fatal(err)
	}
	if n, err := client.Read(response); err == nil || n != 0 {
		t.Fatal("credential rotation retained old connection")
	}
	if _, err := otherClient.Write([]byte("still")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(otherClient, make([]byte, 5)); err != nil {
		t.Fatalf("rotation closed unaffected user: %v", err)
	}
	newClient := connect(proxy.CredentialHash, "example.org", 1)
	defer newClient.Close()
	if _, err := io.ReadFull(newClient, response); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := newClient.Read(response); err == nil {
		t.Fatal("revoked access remained connected")
	}
	if _, err := otherClient.Read(response); err == nil {
		t.Fatal("revoked other user remained connected")
	}
}

func TestTrojanRequiresTLSAndMembershipExpiryClosesSession(t *testing.T) {
	proxy := testProxyConfig()
	proxy.IngressPort = unusedTCPPort(t)
	runtime := New(Options{BindHost: "127.0.0.1"})
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err == nil || runtime.Revision() != 0 {
		t.Fatal("proxy without TLS accepted")
	}
	runtime.Close()
	serverTLS, clientTLS := proxyTestTLS(t)
	runtime = New(Options{BindHost: "127.0.0.1", ProxyTLSConfig: serverTLS, DialTCP: func(context.Context, string) (net.Conn, error) {
		client, peer := net.Pipe()
		go func() { defer peer.Close(); _, _ = io.Copy(peer, peer) }()
		return client, nil
	}})
	defer runtime.Close()
	proxy.ExpiresAt = time.Now().Add(200 * time.Millisecond)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err != nil {
		t.Fatal(err)
	}
	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxy.IngressPort)), clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	conn.Write(append(trojanRequest(proxy.CredentialHash, "8.8.8.8", 443, 1), 'x'))
	if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("expired membership retained session")
	}
}

type retryProxyMeter struct {
	relayCountingMeter
	mu     sync.Mutex
	lineID string
}

func (m *retryProxyMeter) OpenLine(_ context.Context, _, _, lineID string, _ uint64) (MeteredConnection, error) {
	m.opens.Add(1)
	m.mu.Lock()
	m.lineID = lineID
	m.mu.Unlock()
	return &m.relayCountingMeter, nil
}

func TestTrojanRetriesHealthyProxyCandidatesBeforeMetering(t *testing.T) {
	serverTLS, clientTLS := proxyTestTLS(t)
	var attempts atomic.Int64
	failedPeer := make(chan net.Conn, 1)
	meter := &retryProxyMeter{}
	runtime := New(Options{BindHost: "127.0.0.1", ProxyTLSConfig: serverTLS, Meter: meter,
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		DialTCP: func(context.Context, string) (net.Conn, error) {
			if attempts.Add(1) == 1 {
				client, peer := net.Pipe()
				failedPeer <- peer
				return client, errors.New("first candidate is unavailable")
			}
			server, client := net.Pipe()
			go func() { _, _ = io.Copy(server, server); _ = server.Close() }()
			return client, nil
		}})
	defer runtime.Close()
	proxy := testProxyConfig()
	proxy.IngressPort = unusedTCPPort(t)
	proxy.Candidates = []ProxyLineCandidate{
		{LineID: proxy.LineID, Priority: 1, Weight: 1},
		{LineID: "fallback-line", Priority: 2, Weight: 1},
	}
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err != nil {
		t.Fatal(err)
	}
	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxy.IngressPort)), clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(append(trojanRequest(proxy.CredentialHash, "example.org", 443, 1), []byte("ok")...)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(conn, response); err != nil || string(response) != "ok" {
		t.Fatalf("candidate retry response=%q err=%v", response, err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("dial attempts=%d, want 2", attempts.Load())
	}
	peer := <-failedPeer
	defer peer.Close()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("failed candidate connection remained open: %v", err)
	}
	if meter.opens.Load() != 1 {
		t.Fatalf("meter opens=%d, want one post-dial admission", meter.opens.Load())
	}
	meter.mu.Lock()
	lineID := meter.lineID
	meter.mu.Unlock()
	if lineID != "fallback-line" {
		t.Fatalf("metered line=%q, want fallback-line", lineID)
	}
}

func TestTrojanRejectsCandidatePoolWithoutLineAwareMeter(t *testing.T) {
	serverTLS, clientTLS := proxyTestTLS(t)
	meter := &relayCountingMeter{}
	var dials atomic.Int64
	runtime := New(Options{BindHost: "127.0.0.1", ProxyTLSConfig: serverTLS, RequireMetering: true, Meter: meter,
		DialTCP: func(context.Context, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("unexpected dial")
		}})
	defer runtime.Close()
	proxy := testProxyConfig()
	proxy.IngressPort = unusedTCPPort(t)
	proxy.Candidates = []ProxyLineCandidate{{LineID: proxy.LineID, Priority: 1, Weight: 1}, {LineID: "fallback-line", Priority: 2, Weight: 1}}
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err != nil {
		t.Fatal(err)
	}
	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxy.IngressPort)), clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write(trojanRequest(proxy.CredentialHash, "8.8.8.8", 443, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("candidate pool accepted without line-aware meter")
	}
	if dials.Load() != 0 || meter.opens.Load() != 0 {
		t.Fatalf("unattributable candidate traffic attempted: dials=%d opens=%d", dials.Load(), meter.opens.Load())
	}
}

func TestTrojanRejectsSingleFallbackWithoutLineAwareMeter(t *testing.T) {
	serverTLS, clientTLS := proxyTestTLS(t)
	meter := &relayCountingMeter{}
	var dials atomic.Int64
	runtime := New(Options{BindHost: "127.0.0.1", ProxyTLSConfig: serverTLS, RequireMetering: true, Meter: meter,
		DialTCP: func(context.Context, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("unexpected dial")
		}})
	defer runtime.Close()
	proxy := testProxyConfig()
	proxy.IngressPort = unusedTCPPort(t)
	proxy.Candidates = []ProxyLineCandidate{{LineID: "fallback-line", Priority: 1, Weight: 1}}
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err != nil {
		t.Fatal(err)
	}
	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxy.IngressPort)), clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write(trojanRequest(proxy.CredentialHash, "8.8.8.8", 443, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("fallback accepted without line-aware meter")
	}
	if dials.Load() != 0 || meter.opens.Load() != 0 {
		t.Fatalf("unattributable fallback attempted: dials=%d opens=%d", dials.Load(), meter.opens.Load())
	}
}
