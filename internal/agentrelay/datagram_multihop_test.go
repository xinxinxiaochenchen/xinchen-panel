package agentrelay

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestThreeNodeDatagramRelayPreservesPacketBoundaries(t *testing.T) {
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
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		buffer := make([]byte, MaxDatagramPayloadBytes)
		for {
			count, peer, readErr := udp.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			_, _ = udp.WriteToUDP(buffer[:count], peer)
		}
	}()
	expires := time.Now().Add(time.Minute)
	egressRoute := &Route{LineID: testLineID, Generation: 7, PreviousNodeID: middleID,
		PreviousSecret: nextSecret, Context: ctx, ExpiresAt: expires, Window: NewReplayWindow(16)}
	egress := &Handler{Routes: func(string) (*Route, bool) { return egressRoute, true },
		Resolve: func(context.Context, string) (netip.Addr, error) { return netip.MustParseAddr("93.184.215.14"), nil },
		DialDatagramTarget: func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "udp", udp.LocalAddr().String())
		}}
	egressLeft, egressRight := loopbackPair(t)
	defer egressLeft.Close()
	defer egressRight.Close()
	egressDone := make(chan error, 1)
	go func() { egressDone <- egress.HandleConn(ctx, tls.Server(egressRight, egressTLS)) }()
	middleRoute := &Route{LineID: testLineID, Generation: 7, PreviousNodeID: tlsIngressID,
		PreviousSecret: firstSecret, Context: ctx, ExpiresAt: expires, Window: NewReplayWindow(16),
		Next: &NextHop{Address: "approved-egress:24443", TLSConfig: nextTLS, Secret: nextSecret}}
	middle := &Handler{Routes: func(string) (*Route, bool) { return middleRoute, true },
		DialRelay: func(_ context.Context, address string) (net.Conn, error) {
			if address != "approved-egress:24443" {
				t.Errorf("unexpected UDP next hop %q", address)
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
	request := validOpen(time.Now())
	request.Type = "open_udp"
	request, err = SignOpen(request, firstSecret)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if err := WriteOpen(client, request); err != nil {
		t.Fatal(err)
	}
	response, err := ReadOpenResponse(client)
	if err != nil || response.Type != "open_ok" {
		t.Fatalf("UDP multi-hop open = %+v, %v", response, err)
	}
	for _, payload := range [][]byte{[]byte("first"), bytes.Repeat([]byte{9}, 1200), []byte("last")} {
		if err := WriteDatagram(client, payload); err != nil {
			t.Fatal(err)
		}
		got, err := ReadDatagram(client)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("multi-hop UDP payload = %d bytes, %v", len(got), err)
		}
	}
	cancel()
	for _, done := range []chan error{middleDone, egressDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("UDP route cancellation did not stop relay")
		}
	}
}
