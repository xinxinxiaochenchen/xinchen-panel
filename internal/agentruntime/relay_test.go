package agentruntime

import (
	"context"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeRelaysTCPAndRejectsPrivateResolution(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			connection, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer connection.Close(); _, _ = io.Copy(connection, connection) }()
		}
	}()
	var dials atomic.Int64
	resolver := func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "blocked.example" {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	runtime := New(Options{BindHost: "127.0.0.1", Resolve: resolver,
		DialTCP: func(_ context.Context, address string) (net.Conn, error) {
			dials.Add(1)
			if address != "8.8.8.8:443" {
				t.Errorf("dialed unexpected target %q", address)
			}
			return net.Dial("tcp", echo.Addr().String())
		}})
	t.Cleanup(func() { _ = runtime.Close() })
	port := unusedTCPPort(t)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, Rules: []Rule{{ID: "tcp",
		IngressPort: port, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 5)
	if _, err := io.ReadFull(client, buffer); err != nil || string(buffer) != "hello" {
		t.Fatalf("TCP response = %q, %v", buffer, err)
	}
	if dials.Load() != 1 {
		t.Fatalf("TCP dials = %d", dials.Load())
	}
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 2, Rules: []Rule{{ID: "tcp",
		IngressPort: port, TargetHost: "blocked.example", TargetPort: 443, Protocol: "TCP", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	blocked, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	_ = blocked.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = blocked.Write([]byte("deny"))
	if _, err := io.ReadAll(blocked); err != nil && !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("blocked TCP response = %v", err)
	}
	if dials.Load() != 1 {
		t.Fatalf("private DNS answer was dialed: %d", dials.Load())
	}
}

func startUDPEcho(t *testing.T, prefix string) net.PacketConn {
	t.Helper()
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, address, err := server.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = server.WriteTo(append([]byte(prefix), buffer[:n]...), address)
		}
	}()
	return server
}

func TestRuntimeRelaysUDPAndDropsOldAssociationOnTargetChange(t *testing.T) {
	firstEcho := startUDPEcho(t, "one:")
	secondEcho := startUDPEcho(t, "two:")
	var dials atomic.Int64
	resolver := func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "blocked.example" {
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	runtime := New(Options{BindHost: "127.0.0.1", Resolve: resolver,
		DialUDP: func(_ context.Context, address string) (net.Conn, error) {
			dials.Add(1)
			switch address {
			case "8.8.8.8:53":
				return net.Dial("udp", firstEcho.LocalAddr().String())
			case "8.8.8.8:54":
				return net.Dial("udp", secondEcho.LocalAddr().String())
			default:
				t.Errorf("dialed unexpected UDP target %q", address)
				return nil, ErrNonPublicTarget
			}
		}, MaxUDPAssociations: 8, UDPIdleTimeout: time.Second})
	t.Cleanup(func() { _ = runtime.Close() })
	port := unusedTCPPort(t)
	apply := func(revision uint64, host string, targetPort int) {
		t.Helper()
		if err := runtime.Apply(context.Background(), Snapshot{Revision: revision, Rules: []Rule{{ID: "udp",
			IngressPort: port, TargetHost: host, TargetPort: targetPort, Protocol: "UDP", Enabled: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	client, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	request := func(message, expected string) {
		t.Helper()
		_ = client.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := client.Write([]byte(message)); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 128)
		n, err := client.Read(buffer)
		if err != nil || string(buffer[:n]) != expected {
			t.Fatalf("UDP response = %q, %v; want %q", buffer[:n], err, expected)
		}
	}
	apply(1, "example.org", 53)
	request("ping", "one:ping")
	apply(2, "example.org", 54)
	request("ping", "two:ping")
	if dials.Load() != 2 {
		t.Fatalf("UDP associations after target change = %d", dials.Load())
	}
	apply(3, "blocked.example", 53)
	_ = client.SetDeadline(time.Now().Add(150 * time.Millisecond))
	_, _ = client.Write([]byte("blocked"))
	buffer := make([]byte, 128)
	if n, err := client.Read(buffer); err == nil {
		t.Fatalf("private target returned %q", buffer[:n])
	}
	if dials.Load() != 2 {
		t.Fatalf("private UDP answer was dialed: %d", dials.Load())
	}
}

func TestSlowUDPResolutionDoesNotBlockOtherClients(t *testing.T) {
	echo := startUDPEcho(t, "ok:")
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	var resolutions atomic.Int64
	runtime := New(Options{BindHost: "127.0.0.1", Resolve: func(_ context.Context, _ string) ([]netip.Addr, error) {
		if resolutions.Add(1) == 1 {
			close(started)
			<-release
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}, DialUDP: func(_ context.Context, _ string) (net.Conn, error) {
		return net.Dial("udp", echo.LocalAddr().String())
	}})
	t.Cleanup(func() { _ = runtime.Close() })
	port := unusedTCPPort(t)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, Rules: []Rule{{ID: "udp",
		IngressPort: port, TargetHost: "example.org", TargetPort: 53, Protocol: "UDP", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	first, err := net.Dial("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.Dial("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := first.Write([]byte("slow")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first UDP resolution did not start")
	}
	_ = second.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := second.Write([]byte("fast")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	n, err := second.Read(buffer)
	if err != nil || string(buffer[:n]) != "ok:fast" {
		t.Fatalf("second client blocked by slow DNS: %q, %v", buffer[:n], err)
	}
}

func TestTCPRelayReturnsWhenUpstreamClosesWhileClientIsIdle(t *testing.T) {
	ingress, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ingress.Close()
	peer, err := net.Dial("tcp", ingress.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	client, err := ingress.Accept()
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		connection, err := upstream.Accept()
		if err == nil {
			_ = connection.Close()
		}
	}()
	runtime := New(Options{TCPDrainTimeout: 100 * time.Millisecond, Resolve: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}, DialTCP: func(context.Context, string) (net.Conn, error) { return net.Dial("tcp", upstream.Addr().String()) }})
	listener := &endpoint{}
	destination := target{host: "example.org", port: 443}
	listener.target.Store(&destination)
	done := make(chan struct{})
	go func() {
		runtime.relayTCP(listener, newTCPSession(client))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		_ = peer.Close()
		t.Fatal("TCP relay retained an idle client after upstream closed")
	}
}

func TestTCPLimitRejectsExcessConnections(t *testing.T) {
	var dials atomic.Int64
	firstDial := make(chan struct{})
	runtime := New(Options{BindHost: "127.0.0.1", MaxTCPConnections: 1,
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		DialTCP: func(context.Context, string) (net.Conn, error) {
			connection, server := net.Pipe()
			if dials.Add(1) == 1 {
				close(firstDial)
			}
			go func() {
				<-time.After(time.Second)
				_ = server.Close()
			}()
			return connection, nil
		}})
	t.Cleanup(func() { _ = runtime.Close() })
	port := unusedTCPPort(t)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, Rules: []Rule{{ID: "tcp",
		IngressPort: port, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	first, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	select {
	case <-firstDial:
	case <-time.After(time.Second):
		t.Fatal("first TCP connection was not dialed")
	}
	second, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := second.Read(make([]byte, 1)); err == nil {
		t.Fatal("excess TCP connection stayed open")
	}
	if dials.Load() != 1 {
		t.Fatalf("excess TCP connection reached target: %d dials", dials.Load())
	}
}

func TestDisablingTCPRuleClosesActiveStream(t *testing.T) {
	dialed := make(chan struct{})
	runtime := New(Options{BindHost: "127.0.0.1", Resolve: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}, DialTCP: func(context.Context, string) (net.Conn, error) {
		connection, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		close(dialed)
		return connection, nil
	}})
	t.Cleanup(func() { _ = runtime.Close() })
	port := unusedTCPPort(t)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, Rules: []Rule{{ID: "tcp",
		IngressPort: port, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	select {
	case <-dialed:
	case <-time.After(time.Second):
		t.Fatal("TCP stream was not established")
	}
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 2}); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("disabled rule retained active stream")
	} else if networkError, ok := err.(net.Error); ok && networkError.Timeout() {
		t.Fatal("disabled rule did not close active stream")
	}
}

func TestTCPRelayReturnsWhenClientHalfClosesAndUpstreamIsIdle(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		connection, err := upstream.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(io.Discard, connection)
		<-time.After(time.Second)
	}()
	runtime := New(Options{BindHost: "127.0.0.1", TCPDrainTimeout: 100 * time.Millisecond,
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}, DialTCP: func(context.Context, string) (net.Conn, error) {
			return net.Dial("tcp", upstream.Addr().String())
		}})
	t.Cleanup(func() { _ = runtime.Close() })
	port := unusedTCPPort(t)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, Rules: []Rule{{ID: "tcp",
		IngressPort: port, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("idle upstream kept half-closed client connected")
	} else if networkError, ok := err.(net.Error); ok && networkError.Timeout() {
		t.Fatal("idle upstream retained TCP slot beyond drain timeout")
	}
}

func TestUDPAssociationExpiresDespiteContinuousTargetReplies(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	stopReplies := make(chan struct{})
	defer close(stopReplies)
	go func() {
		buffer := make([]byte, 32)
		_, address, err := server.ReadFrom(buffer)
		if err != nil {
			return
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _ = server.WriteTo([]byte("spam"), address)
			case <-stopReplies:
				return
			}
		}
	}()
	runtime := New(Options{BindHost: "127.0.0.1", UDPIdleTimeout: 80 * time.Millisecond,
		MaxUDPAssociations: 1, Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}, DialUDP: func(context.Context, string) (net.Conn, error) {
			return net.Dial("udp", server.LocalAddr().String())
		}})
	t.Cleanup(func() { _ = runtime.Close() })
	port := unusedTCPPort(t)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, Rules: []Rule{{ID: "udp",
		IngressPort: port, TargetHost: "example.org", TargetPort: 53, Protocol: "UDP", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("start")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	observed := false
	for time.Now().Before(deadline) {
		runtime.mu.Lock()
		listener := runtime.listeners[listenerKey{protocol: "UDP", port: port}]
		runtime.mu.Unlock()
		listener.udpMu.Lock()
		count := len(listener.sessions)
		listener.udpMu.Unlock()
		if count == 1 {
			observed = true
		}
		if observed && count == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("UDP association retained despite no client packets beyond idle timeout")
}

type delayedUDPRead struct {
	net.Conn
	readDone chan struct{}
	resume   chan struct{}
}

func (connection *delayedUDPRead) Read(buffer []byte) (int, error) {
	count, err := connection.Conn.Read(buffer)
	if err == nil {
		close(connection.readDone)
		<-connection.resume
	}
	return count, err
}

func TestUDPReplyFromPreviousTargetIsDroppedAfterApply(t *testing.T) {
	readDone := make(chan struct{})
	resume := make(chan struct{})
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	runtime := New(Options{BindHost: "127.0.0.1", Resolve: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}, DialUDP: func(context.Context, string) (net.Conn, error) {
		client, peer := net.Pipe()
		go func() {
			defer peer.Close()
			buffer := make([]byte, 16)
			if _, err := peer.Read(buffer); err == nil {
				_, _ = peer.Write([]byte("old"))
			}
		}()
		return &delayedUDPRead{Conn: client, readDone: readDone, resume: resume}, nil
	}})
	t.Cleanup(func() { _ = runtime.Close() })
	port := unusedTCPPort(t)
	apply := func(revision uint64, targetPort int) {
		t.Helper()
		if err := runtime.Apply(context.Background(), Snapshot{Revision: revision, Rules: []Rule{{ID: "udp",
			IngressPort: port, TargetHost: "example.org", TargetPort: targetPort, Protocol: "UDP", Enabled: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	apply(1, 53)
	client, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("old target did not produce a reply")
	}
	apply(2, 54)
	close(resume)
	_ = client.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if count, err := client.Read(make([]byte, 16)); err == nil {
		t.Fatalf("received %d stale reply bytes after target change", count)
	}
}

func TestCloseWaitsForUDPReplyLoop(t *testing.T) {
	readDone := make(chan struct{})
	resume := make(chan struct{})
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	runtime := New(Options{BindHost: "127.0.0.1", Resolve: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}, DialUDP: func(context.Context, string) (net.Conn, error) {
		client, peer := net.Pipe()
		go func() {
			defer peer.Close()
			buffer := make([]byte, 16)
			if _, err := peer.Read(buffer); err == nil {
				_, _ = peer.Write([]byte("reply"))
			}
		}()
		return &delayedUDPRead{Conn: client, readDone: readDone, resume: resume}, nil
	}})
	t.Cleanup(func() { _ = runtime.Close() })
	port := unusedTCPPort(t)
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, Rules: []Rule{{ID: "udp",
		IngressPort: port, TargetHost: "example.org", TargetPort: 53, Protocol: "UDP", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("UDP reply loop did not start")
	}
	done := make(chan struct{})
	go func() {
		_ = runtime.Close()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Close returned while UDP reply loop was active")
	case <-time.After(30 * time.Millisecond):
	}
	close(resume)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after UDP reply loop stopped")
	}
}
