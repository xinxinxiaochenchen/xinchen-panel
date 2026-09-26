package agentruntime

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"controlplane/internal/agentmeter"
)

type waitingSettlementMeter struct {
	started chan struct{}
	ack     chan struct{}
}

func (m *waitingSettlementMeter) Copy(io.Writer, io.Reader, agentmeter.Direction) (int64, error) {
	return 0, nil
}

func (m *waitingSettlementMeter) WritePacket(io.Writer, []byte, agentmeter.Direction) (int, error) {
	return 0, nil
}

func (m *waitingSettlementMeter) Close() error {
	close(m.started)
	<-m.ack
	return nil
}

func TestApplyRevokesMeteredTCPSessionWithoutWaitingForSettlement(t *testing.T) {
	runtime := New(Options{})
	port := 12345
	key := listenerKey{protocol: "TCP", port: port}
	listener := &endpoint{key: key, tcpSessions: make(map[*tcpSession]struct{})}
	old := target{host: "example.org", port: 443, id: "old", revision: 1}
	listener.target.Store(&old)
	runtime.listeners[key] = listener
	runtime.revision = 1

	client, peer := net.Pipe()
	defer peer.Close()
	session := newTCPSession(client)
	meter := &waitingSettlementMeter{started: make(chan struct{}), ack: make(chan struct{})}
	defer close(meter.ack)
	if !session.setMeter(meter) {
		t.Fatal("failed to attach meter")
	}
	listener.tcpSessions[session] = struct{}{}

	result := make(chan error, 1)
	go func() {
		result <- runtime.Apply(context.Background(), Snapshot{Revision: 2, Rules: []Rule{{
			ID: "new", IngressPort: port, TargetHost: "example.org", TargetPort: 8443,
			Protocol: "TCP", Enabled: true,
		}}})
	}()
	select {
	case <-meter.started:
	case <-time.After(time.Second):
		t.Fatal("revoked meter was not closed")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("apply failed: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("apply waited for settlement ACK and blocked its own control stream")
	}
}
