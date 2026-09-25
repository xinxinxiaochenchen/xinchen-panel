package agentruntime

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

func unusedTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestRuntimeApplyRollsBackOnBindFailure(t *testing.T) {
	firstPort := unusedTCPPort(t)
	secondPort := unusedTCPPort(t)
	for secondPort == firstPort {
		secondPort = unusedTCPPort(t)
	}
	runtime := New(Options{BindHost: "127.0.0.1"})
	t.Cleanup(func() { _ = runtime.Close() })
	first := Snapshot{Revision: 1, Rules: []Rule{{ID: "first", IngressPort: firstPort,
		TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true}}}
	if err := runtime.Apply(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	blocker, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(secondPort)))
	if err != nil {
		t.Fatal(err)
	}
	second := Snapshot{Revision: 2, Rules: []Rule{
		first.Rules[0],
		{ID: "second", IngressPort: secondPort, TargetHost: "example.org", TargetPort: 443, Protocol: "BOTH", Enabled: true},
	}}
	if err := runtime.Apply(context.Background(), second); err == nil || runtime.Revision() != 1 {
		t.Fatalf("failed apply changed revision: revision=%d error=%v", runtime.Revision(), err)
	}
	connection, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(firstPort)))
	if err != nil {
		t.Fatalf("old listener unavailable after failed apply: %v", err)
	}
	_ = connection.Close()
	_ = blocker.Close()
	if err := runtime.Apply(context.Background(), second); err != nil || runtime.Revision() != 2 {
		t.Fatalf("second snapshot = revision %d, %v", runtime.Revision(), err)
	}
	if _, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(secondPort))); err == nil {
		t.Fatal("BOTH snapshot did not bind UDP port")
	}
	if err := runtime.Apply(context.Background(), second); err == nil {
		t.Fatal("equal revision accepted")
	}
	if err := runtime.Apply(context.Background(), first); err == nil {
		t.Fatal("stale revision accepted")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(firstPort))); err != nil {
		t.Fatalf("Close retained TCP port: %v", err)
	} else {
		_ = listener.Close()
	}
}

func TestConcurrentCloseCallsBothWaitForRelayLoops(t *testing.T) {
	runtime := New(Options{})
	runtime.loops.Add(1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	go func() {
		<-release
		runtime.loops.Done()
	}()
	firstDone := make(chan struct{})
	go func() {
		_ = runtime.Close()
		close(firstDone)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		runtime.mu.Lock()
		closed := runtime.closed
		runtime.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first Close did not begin")
		}
		time.Sleep(time.Millisecond)
	}
	secondDone := make(chan struct{})
	go func() {
		_ = runtime.Close()
		close(secondDone)
	}()
	select {
	case <-secondDone:
		t.Fatal("second Close returned before relay loops stopped")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	for _, done := range []<-chan struct{}{firstDone, secondDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Close did not finish after relay loop stopped")
		}
	}
}
