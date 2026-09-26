package agentruntime

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"controlplane/internal/agentmeter"
)

type waitingMeteredSession struct {
	closed chan struct{}
}

func (m *waitingMeteredSession) Copy(_ io.Writer, _ io.Reader, direction agentmeter.Direction) (int64, error) {
	if direction == agentmeter.Upload {
		return 0, errors.New("upload failed")
	}
	<-m.closed
	return 0, errors.New("meter closed")
}

func (m *waitingMeteredSession) WritePacket(io.Writer, []byte, agentmeter.Direction) (int, error) {
	return 0, errors.New("unused")
}

func (m *waitingMeteredSession) Close() error {
	select {
	case <-m.closed:
	default:
		close(m.closed)
	}
	return nil
}

func TestProxyRelayClosesMeterWhenOneDirectionFails(t *testing.T) {
	client, clientPeer := net.Pipe()
	upstream, upstreamPeer := net.Pipe()
	defer clientPeer.Close()
	defer upstreamPeer.Close()
	session := newTCPSession(client)
	session.upstream = upstream
	meter := &waitingMeteredSession{closed: make(chan struct{})}
	if !session.setMeter(meter) {
		t.Fatal("meter rejected")
	}
	done := make(chan struct{})
	go func() {
		relayProxyStreams(session, bytes.NewReader(nil), time.Second, meter)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy relay waited forever for metered direction")
	}
}
