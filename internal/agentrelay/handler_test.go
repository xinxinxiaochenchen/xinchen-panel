package agentrelay

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestEgressHandlerAuthenticatesAndForwardsTCP(t *testing.T) {
	roots, clientCert, serverCert := relayTLSFixture(t)
	serverTLS, err := ServerTLSConfig(func() (*tls.Certificate, error) { return &serverCert, nil }, roots,
		func(id, fingerprint string) bool {
			return id == tlsIngressID && fingerprint == certFingerprint(clientCert)
		})
	if err != nil {
		t.Fatal(err)
	}
	clientTLS, err := ClientTLSConfig(func() (*tls.Certificate, error) { return &clientCert, nil }, roots,
		"relay.example.com", tlsEgressID, []string{certFingerprint(serverCert)})
	if err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte{0x32}, 32)
	routeContext, revoke := context.WithCancel(context.Background())
	defer revoke()
	route := &Route{LineID: testLineID, Generation: 7, PreviousNodeID: tlsIngressID,
		PreviousSecret: secret, Window: NewReplayWindow(16), Context: routeContext, ExpiresAt: time.Now().Add(time.Minute)}
	echoClient, echoServer := net.Pipe()
	defer echoClient.Close()
	defer echoServer.Close()
	go func() { _, _ = io.Copy(echoServer, echoServer) }()
	var observedAddress string
	handler := &Handler{
		Routes: func(lineID string) (*Route, bool) {
			if lineID != testLineID {
				return nil, false
			}
			return route, true
		},
		Resolve: func(_ context.Context, host string) (netip.Addr, error) {
			if host != "example.com" {
				return netip.Addr{}, errors.New("wrong target")
			}
			return netip.MustParseAddr("93.184.215.14"), nil
		},
		DialTarget: func(_ context.Context, address string) (net.Conn, error) {
			observedAddress = address
			return echoClient, nil
		},
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	server := tls.Server(right, serverTLS)
	finished := make(chan error, 1)
	go func() { finished <- handler.HandleConn(context.Background(), server) }()
	client := tls.Client(left, clientTLS)
	defer closeImmediately(client)
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	request, err := SignOpen(validOpen(time.Now()), secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteOpen(client, request); err != nil {
		t.Fatal(err)
	}
	response, err := ReadOpenResponse(client)
	if err != nil || response.Type != "open_ok" || response.ConnectionID != request.ConnectionID {
		t.Fatalf("relay response = %+v, %v", response, err)
	}
	if observedAddress != "93.184.215.14:443" {
		t.Fatalf("egress dialed %q", observedAddress)
	}
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	var reply [5]byte
	if _, err := io.ReadFull(client, reply[:]); err != nil || string(reply[:]) != "hello" {
		t.Fatalf("egress reply = %q, %v", reply, err)
	}
	revoke()
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(reply[:]); err == nil {
		t.Fatal("revoked route still has an open client stream")
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("egress handler did not stop after route revoked")
	}
}

func TestEgressHandlerRejectsPrivateResolutionBeforeDial(t *testing.T) {
	roots, clientCert, serverCert := relayTLSFixture(t)
	serverTLS, _ := ServerTLSConfig(func() (*tls.Certificate, error) { return &serverCert, nil }, roots,
		func(string, string) bool { return true })
	clientTLS, _ := ClientTLSConfig(func() (*tls.Certificate, error) { return &clientCert, nil }, roots,
		"relay.example.com", tlsEgressID, []string{certFingerprint(serverCert)})
	secret := bytes.Repeat([]byte{0x32}, 32)
	handler := &Handler{Routes: func(string) (*Route, bool) {
		return &Route{LineID: testLineID, Generation: 7,
			PreviousNodeID: tlsIngressID, PreviousSecret: secret, Window: NewReplayWindow(16), Context: context.Background(), ExpiresAt: time.Now().Add(time.Minute)}, true
	},
		Resolve:    func(context.Context, string) (netip.Addr, error) { return netip.MustParseAddr("127.0.0.1"), nil },
		DialTarget: func(context.Context, string) (net.Conn, error) { t.Fatal("private target dialed"); return nil, nil }}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go func() { _ = handler.HandleConn(context.Background(), tls.Server(right, serverTLS)) }()
	client := tls.Client(left, clientTLS)
	defer closeImmediately(client)
	request, _ := SignOpen(validOpen(time.Now()), secret)
	if err := WriteOpen(client, request); err != nil {
		t.Fatal(err)
	}
	response, err := ReadOpenResponse(client)
	if err != nil || response.Type != "open_err" || response.ErrorCode != "TARGET_REJECTED" {
		t.Fatalf("private resolution response = %+v, %v", response, err)
	}
}
