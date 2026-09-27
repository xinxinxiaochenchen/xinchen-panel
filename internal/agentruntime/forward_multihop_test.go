package agentruntime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"controlplane/internal/agentrelay"
)

func TestRelayDatagramConnSerializesConcurrentPackets(t *testing.T) {
	base, peer := net.Pipe()
	defer base.Close()
	defer peer.Close()
	collector := &interleavingConn{Conn: base, firstHeader: make(chan struct{})}
	conn := &relayDatagramConn{relayForwardConn: relayForwardConn{Conn: collector}}
	first := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("first")); first <- err }()
	<-collector.firstHeader
	second := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("second")); second <- err }()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	collector.mu.Lock()
	wire := append([]byte(nil), collector.buffer.Bytes()...)
	collector.mu.Unlock()
	reader := bytes.NewReader(wire)
	for _, want := range []string{"first", "second"} {
		packet, err := agentrelay.ReadDatagram(reader)
		if err != nil || string(packet) != want {
			t.Fatalf("packet=%q, want=%q, err=%v", packet, want, err)
		}
	}
}

type interleavingConn struct {
	net.Conn
	mu          sync.Mutex
	buffer      bytes.Buffer
	firstHeader chan struct{}
	once        sync.Once
}

func (c *interleavingConn) Write(payload []byte) (int, error) {
	if len(payload) == 4 {
		first := false
		c.once.Do(func() { first = true; close(c.firstHeader) })
		if first {
			time.Sleep(30 * time.Millisecond)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer.Write(payload)
}

func TestRelayForwardConnPassesThroughTCPHalfClose(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	inner := &halfCloseTestConn{Conn: client}
	wrapped := relayForwardConn{Conn: inner}
	closeWrite(wrapped)
	if !inner.writeClosed.Load() {
		t.Fatal("relay forward did not propagate TCP half-close")
	}
	result := make(chan error, 1)
	go func() { _, err := server.Write([]byte("reply")); result <- err }()
	buffer := make([]byte, 5)
	_ = wrapped.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(wrapped, buffer); err != nil || string(buffer) != "reply" {
		t.Fatalf("response after half-close=%q, err=%v", buffer, err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

type halfCloseTestConn struct {
	net.Conn
	writeClosed atomic.Bool
}

func (c *halfCloseTestConn) CloseWrite() error {
	c.writeClosed.Store(true)
	return nil
}

func TestRelayDatagramConnRetainsPartialFrameAfterReadTimeout(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := &relayDatagramConn{relayForwardConn: relayForwardConn{Conn: client}}
	result := make(chan error, 1)
	go func() {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], 4)
		_, _ = server.Write(header[:2])
		time.Sleep(50 * time.Millisecond)
		_, err := server.Write(append(header[2:], []byte("test")...))
		result <- err
	}()
	_ = conn.SetReadDeadline(time.Now().Add(25 * time.Millisecond))
	buffer := make([]byte, 16)
	if _, err := conn.Read(buffer); err == nil {
		t.Fatal("partial frame unexpectedly completed before timeout")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if count, err := conn.Read(buffer); err != nil || string(buffer[:count]) != "test" {
		t.Fatalf("resumed frame=%q, err=%v", buffer[:count], err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestLineBoundUDPForwardUsesRelay(t *testing.T) {
	roots, issue := runtimeRelayAuthority(t)
	const ingressID = "11111111-1111-4111-8111-111111111111"
	const egressID = "33333333-3333-4333-8333-333333333333"
	const lineID = "44444444-4444-4444-8444-444444444444"
	clientCert, serverCert := issue(ingressID, false), issue(egressID, true)
	serverTLS, err := agentrelay.ServerTLSConfig(func() (*tls.Certificate, error) { return &serverCert, nil }, roots, func(id, fingerprint string) bool {
		return id == ingressID && fingerprint == runtimeFingerprint(clientCert)
	})
	if err != nil {
		t.Fatal(err)
	}
	clientTLS, err := agentrelay.ClientTLSConfig(func() (*tls.Certificate, error) { return &clientCert, nil }, roots, "127.0.0.1", egressID, []string{runtimeFingerprint(serverCert)})
	if err != nil {
		t.Fatal(err)
	}
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buffer := make([]byte, 2048)
		for {
			count, peer, readErr := echo.ReadFrom(buffer)
			if readErr != nil {
				return
			}
			_, _ = echo.WriteTo(buffer[:count], peer)
		}
	}()
	secret := bytes.Repeat([]byte{9}, 32)
	egressPort := unusedTCPPort(t)
	egress := New(Options{BindHost: "127.0.0.1", RelayPort: egressPort, RelayTLSConfig: serverTLS, Resolve: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}, DialUDP: func(ctx context.Context, address string) (net.Conn, error) {
		if address != "8.8.8.8:53" {
			return nil, errors.New("unexpected target")
		}
		return (&net.Dialer{}).DialContext(ctx, "udp", echo.LocalAddr().String())
	}})
	defer egress.Close()
	if err := egress.Apply(context.Background(), Snapshot{Revision: 1, RelayConfig: []RelayConfig{{LineID: lineID, Generation: 1, Role: RelayEgress, PreviousNodeID: ingressID, PreviousSecret: secret, PreviousFingerprints: []string{runtimeFingerprint(clientCert)}}}}); err != nil {
		t.Fatal(err)
	}
	forwardPort := unusedTCPPort(t)
	ingress := New(Options{BindHost: "127.0.0.1", RelayPort: unusedTCPPort(t), RelayTLSConfig: serverTLS, RelayClientTLS: func(context.Context, RelayNextHop) (*tls.Config, error) { return clientTLS, nil }, DialUDP: func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("ingress attempted direct UDP dial")
	}})
	defer ingress.Close()
	ingressRoute := RelayConfig{LineID: lineID, Generation: 1, Role: RelayIngress, Next: &RelayNextHop{NodeID: egressID, Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(egressPort)), Port: egressPort, Secret: secret, Fingerprints: []string{runtimeFingerprint(serverCert)}}}
	rule := Rule{ID: "udp-forward", IngressPort: forwardPort, TargetHost: "example.org", TargetPort: 53, Protocol: "UDP", Enabled: true, LineID: lineID, RelayGeneration: 1}
	if err := ingress.Apply(context.Background(), Snapshot{Revision: 1, Rules: []Rule{rule}, RelayConfig: []RelayConfig{ingressRoute}}); err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(forwardPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write([]byte("relayed UDP")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 64)
	count, err := client.Read(response)
	if err != nil || string(response[:count]) != "relayed UDP" {
		t.Fatalf("UDP response=%q, err=%v", response[:count], err)
	}
}
