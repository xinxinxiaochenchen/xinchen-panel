package agentruntime

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"controlplane/internal/agentmeter"
)

type testTrafficMeter struct {
	mu          sync.Mutex
	deny        bool
	openedKind  string
	openedID    string
	openedRev   uint64
	openedCount int
	last        *testMeteredSession
}

type testMeteredSession struct {
	budget *agentmeter.Budget
	closed atomic.Bool
}

func (m *testTrafficMeter) Open(_ context.Context, kind, resourceID string, revision uint64) (MeteredConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openedKind, m.openedID, m.openedRev = kind, resourceID, revision
	m.openedCount++
	if m.deny {
		return nil, errors.New("quota denied")
	}
	budget, err := agentmeter.NewBudget(1000, 4, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		return nil, err
	}
	m.last = &testMeteredSession{budget: budget}
	return m.last, nil
}

func (s *testMeteredSession) Copy(dst io.Writer, src io.Reader, direction agentmeter.Direction) (int64, error) {
	return agentmeter.CopyMetered(dst, src, s.budget, direction)
}
func (s *testMeteredSession) WritePacket(dst io.Writer, payload []byte, direction agentmeter.Direction) (int, error) {
	return agentmeter.WritePacket(dst, payload, s.budget, direction)
}
func (s *testMeteredSession) Close() error { s.closed.Store(true); return nil }

func TestRuntimeTCPRequiresQuotaBeforeDialAndCapsBothDirections(t *testing.T) {
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
	meter := &testTrafficMeter{deny: true}
	var dials atomic.Int64
	runtime := New(Options{BindHost: "127.0.0.1", RequireMetering: true, Meter: meter,
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		DialTCP: func(context.Context, string) (net.Conn, error) {
			dials.Add(1)
			return net.Dial("tcp", echo.Addr().String())
		}})
	defer runtime.Close()
	port := unusedTCPPort(t)
	const resourceID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if err := runtime.Apply(context.Background(), Snapshot{Revision: 1, Rules: []Rule{{ID: resourceID,
		IngressPort: port, TargetHost: "example.org", TargetPort: 443, Protocol: "TCP", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	denied, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	_ = denied.SetDeadline(time.Now().Add(time.Second))
	_, _ = denied.Write([]byte("blocked"))
	_, _ = io.ReadAll(denied)
	denied.Close()
	if dials.Load() != 0 {
		t.Fatalf("dialed upstream before admission: %d", dials.Load())
	}
	meter.mu.Lock()
	meter.deny = false
	meter.mu.Unlock()
	allowed, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer allowed.Close()
	_ = allowed.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := allowed.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 1)
	if _, err := io.ReadFull(allowed, response); err != nil || string(response) != "a" {
		t.Fatalf("quota limited response=%q err=%v", response, err)
	}
	_ = allowed.Close()
	meter.mu.Lock()
	last, kind, id, revision := meter.last, meter.openedKind, meter.openedID, meter.openedRev
	meter.mu.Unlock()
	if last == nil || kind != "forward" || id != resourceID || revision != 1 || dials.Load() != 1 {
		t.Fatalf("meter admission kind=%s id=%s revision=%d dials=%d", kind, id, revision, dials.Load())
	}
	state := last.budget.Snapshot()
	if state.UploadedBytes+state.DownloadedBytes > 4 || state.ChargedBytes > 4 {
		t.Fatalf("over quota state=%+v", state)
	}
}
