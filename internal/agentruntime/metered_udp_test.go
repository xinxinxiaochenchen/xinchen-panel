package agentruntime

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeUDPRequiresAdmissionAndDropsOverQuotaDatagram(t *testing.T) {
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, client, err := echo.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = echo.WriteTo(buffer[:n], client)
		}
	}()
	meter := &testTrafficMeter{deny: true}
	var dials atomic.Int64
	runtime := New(Options{BindHost: "127.0.0.1", RequireMetering: true, Meter: meter,
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		DialUDP: func(context.Context, string) (net.Conn, error) {
			dials.Add(1)
			return net.Dial("udp", echo.LocalAddr().String())
		}})
	defer runtime.Close()
	port := unusedTCPPort(t)
	const resourceID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, Rules: []Rule{{ID: resourceID,
		IngressPort: port, TargetHost: "example.org", TargetPort: 443, Protocol: "UDP", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	buffer := make([]byte, 10)
	_ = client.SetDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := client.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if n, err := client.Read(buffer); err == nil || n != 0 || dials.Load() != 0 {
		t.Fatalf("unauthorized UDP reply=%d %v dials=%d", n, err, dials.Load())
	}
	meter.mu.Lock()
	meter.deny = false
	meter.mu.Unlock()
	_ = client.SetDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := client.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if n, err := client.Read(buffer); err == nil || n != 0 {
		t.Fatalf("over quota UDP reply=%q %v", buffer[:n], err)
	}
	meter.mu.Lock()
	last, kind, id, revision := meter.last, meter.openedKind, meter.openedID, meter.openedRev
	meter.mu.Unlock()
	if last == nil || kind != "forward" || id != resourceID || revision != 1 || dials.Load() != 1 {
		t.Fatalf("UDP admission kind=%s id=%s rev=%d dials=%d", kind, id, revision, dials.Load())
	}
	if state := last.budget.Snapshot(); state.UploadedBytes != 3 || state.DownloadedBytes != 0 || state.ChargedBytes != 3 {
		t.Fatalf("UDP datagram was partially sent: %+v", state)
	}
}
