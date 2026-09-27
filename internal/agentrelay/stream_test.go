package agentrelay

import (
	"crypto/tls"
	"net"
	"testing"
	"time"
)

func TestImmediateCloseDoesNotWaitForUnreadTLSAlert(t *testing.T) {
	roots, clientPair, serverPair := relayTLSFixture(t)
	clientConfig, _ := ClientTLSConfig(func() (*tls.Certificate, error) { return &clientPair, nil }, roots,
		"relay.example.com", tlsEgressID, []string{certFingerprint(serverPair)})
	serverConfig, _ := ServerTLSConfig(func() (*tls.Certificate, error) { return &serverPair, nil }, roots, func(string, string) bool { return true })
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	client := tls.Client(left, clientConfig)
	server := tls.Server(right, serverConfig)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	closeImmediately(client)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("immediate close waited for peer: %v", elapsed)
	}
}
