package agentruntime

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestTrojanMeterStartsAfterHandshakeAndRejectsBeforeDial(t *testing.T) {
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
	meter := &testTrafficMeter{deny: true}
	var dials atomic.Int64
	runtime := New(Options{BindHost: "127.0.0.1", ProxyTLSConfig: serverTLS, RequireMetering: true, Meter: meter,
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		DialTCP: func(context.Context, string) (net.Conn, error) {
			dials.Add(1)
			return net.Dial("tcp", echo.Addr().String())
		}})
	defer runtime.Close()
	proxy := testProxyConfig()
	proxy.IngressPort = unusedTCPPort(t)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err != nil {
		t.Fatal(err)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(proxy.IngressPort))
	denied, err := tls.Dial("tcp", address, clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	_ = denied.SetDeadline(time.Now().Add(time.Second))
	_, _ = denied.Write(append(trojanRequest(proxy.CredentialHash, "example.org", 443, 1), []byte("abc")...))
	_, _ = denied.Read(make([]byte, 1))
	denied.Close()
	if dials.Load() != 0 {
		t.Fatal("Trojan dialed target before quota admission")
	}
	meter.mu.Lock()
	meter.deny = false
	meter.mu.Unlock()
	allowed, err := tls.Dial("tcp", address, clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer allowed.Close()
	_ = allowed.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := allowed.Write(append(trojanRequest(proxy.CredentialHash, "example.org", 443, 1), []byte("abc")...)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 1)
	if _, err := io.ReadFull(allowed, response); err != nil || string(response) != "a" {
		t.Fatalf("Trojan limited reply=%q %v", response, err)
	}
	allowed.Close()
	meter.mu.Lock()
	last, kind, resourceID, revision := meter.last, meter.openedKind, meter.openedID, meter.openedRev
	meter.mu.Unlock()
	if last == nil || kind != "proxy" || resourceID != proxy.ID || revision != 1 || dials.Load() != 1 {
		t.Fatalf("Trojan meter kind=%s resource=%s revision=%d dials=%d", kind, resourceID, revision, dials.Load())
	}
	state := last.budget.Snapshot()
	if state.UploadedBytes != 3 || state.DownloadedBytes > 1 || state.ChargedBytes > 4 {
		t.Fatalf("handshake bytes entered quota: %+v", state)
	}
}
