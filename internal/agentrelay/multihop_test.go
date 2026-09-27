package agentrelay

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestThreeNodeRelayUsesConfiguredNextHop(t *testing.T) {
	roots, issue := relayTLSAuthority(t)
	const middleID = "33333333-3333-4333-8333-333333333333"
	ingressCert := issue(tlsIngressID, false)
	middleClient, middleServer := issue(middleID, false), issue(middleID, true)
	egressServer := issue(tlsEgressID, true)
	get := func(pair tls.Certificate) func() (*tls.Certificate, error) {
		return func() (*tls.Certificate, error) { return &pair, nil }
	}
	ingressTLS, _ := ClientTLSConfig(get(ingressCert), roots, "relay.example.com", middleID, []string{certFingerprint(middleServer)})
	middleTLS, _ := ServerTLSConfig(get(middleServer), roots, func(id, fingerprint string) bool {
		return id == tlsIngressID && fingerprint == certFingerprint(ingressCert)
	})
	nextTLS, _ := ClientTLSConfig(get(middleClient), roots, "relay.example.com", tlsEgressID, []string{certFingerprint(egressServer)})
	egressTLS, _ := ServerTLSConfig(get(egressServer), roots, func(id, fingerprint string) bool {
		return id == middleID && fingerprint == certFingerprint(middleClient)
	})
	firstSecret, nextSecret := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	expires := time.Now().Add(time.Minute)
	egressRoute := &Route{LineID: testLineID, Generation: 7, PreviousNodeID: middleID, PreviousSecret: nextSecret,
		Context: ctx, ExpiresAt: expires, Window: NewReplayWindow(16)}
	echoClient, echoServer := loopbackPair(t)
	defer echoClient.Close()
	defer echoServer.Close()
	go func() { _, _ = io.Copy(echoServer, echoServer) }()
	egress := &Handler{Routes: func(string) (*Route, bool) { return egressRoute, true },
		Resolve:    func(context.Context, string) (netip.Addr, error) { return netip.MustParseAddr("93.184.215.14"), nil },
		DialTarget: func(context.Context, string) (net.Conn, error) { return echoClient, nil }}
	egressLeft, egressRight := loopbackPair(t)
	defer egressLeft.Close()
	defer egressRight.Close()
	egressDone := make(chan error, 1)
	go func() { egressDone <- egress.HandleConn(ctx, tls.Server(egressRight, egressTLS)) }()
	middleRoute := &Route{LineID: testLineID, Generation: 7, PreviousNodeID: tlsIngressID, PreviousSecret: firstSecret,
		Context: ctx, ExpiresAt: expires, Window: NewReplayWindow(16),
		Next: &NextHop{Address: "approved-egress:24443", TLSConfig: nextTLS, Secret: nextSecret}}
	middle := &Handler{Routes: func(string) (*Route, bool) { return middleRoute, true },
		DialRelay: func(_ context.Context, address string) (net.Conn, error) {
			if address != "approved-egress:24443" {
				t.Errorf("relay dialed unconfigured next hop %q", address)
			}
			return egressLeft, nil
		}}
	left, right := loopbackPair(t)
	defer left.Close()
	defer right.Close()
	middleDone := make(chan error, 1)
	go func() { middleDone <- middle.HandleConn(ctx, tls.Server(right, middleTLS)) }()
	client := tls.Client(left, ingressTLS)
	defer closeImmediately(client)
	request, _ := SignOpen(validOpen(time.Now()), firstSecret)
	if err := WriteOpen(client, request); err != nil {
		t.Fatal(err)
	}
	response, err := ReadOpenResponse(client)
	if err != nil || response.Type != "open_ok" {
		t.Fatalf("three-node open = %+v, %v", response, err)
	}
	if _, err := client.Write([]byte("three-hop")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 9)
	if _, err := io.ReadFull(client, got); err != nil || string(got) != "three-hop" {
		t.Fatalf("three-node payload = %q, %v", got, err)
	}
	cancel()
	for _, done := range []chan error{middleDone, egressDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("route cancellation did not close entire chain")
		}
	}
}

func loopbackPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.Accept()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}
