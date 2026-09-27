package agentruntime

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"controlplane/internal/agentrelay"
	"controlplane/internal/platform/id"
)

// relayDatagramConn adapts one authenticated framed relay stream to the
// connected packet interface used by the existing UDP association runtime.
type relayDatagramConn struct {
	relayForwardConn
	header      [4]byte
	headerRead  int
	payload     []byte
	payloadRead int
	writeMu     sync.Mutex
}

type relayForwardConn struct {
	net.Conn
	stop func() bool
}

func (c relayForwardConn) CloseWrite() error {
	if half, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return c.Conn.Close()
}

func (c relayForwardConn) Close() error {
	if c.stop != nil {
		c.stop()
	}
	return c.Conn.Close()
}

func (c *relayDatagramConn) Write(payload []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := agentrelay.WriteDatagram(c.Conn, payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (c *relayDatagramConn) Read(buffer []byte) (int, error) {
	for c.headerRead < len(c.header) {
		count, err := c.Conn.Read(c.header[c.headerRead:])
		c.headerRead += count
		if err != nil {
			return 0, err
		}
		if count == 0 {
			return 0, io.ErrNoProgress
		}
	}
	if c.payload == nil {
		length := binary.BigEndian.Uint32(c.header[:])
		if length > agentrelay.MaxDatagramPayloadBytes {
			return 0, errors.New("relay datagram payload is too large")
		}
		c.payload = make([]byte, int(length))
	}
	for c.payloadRead < len(c.payload) {
		count, err := c.Conn.Read(c.payload[c.payloadRead:])
		c.payloadRead += count
		if err != nil {
			return 0, err
		}
		if count == 0 {
			return 0, io.ErrNoProgress
		}
	}
	frame := c.payload
	c.headerRead, c.payloadRead, c.payload = 0, 0, nil
	if len(frame) > len(buffer) {
		return 0, io.ErrShortBuffer
	}
	copy(buffer, frame)
	return len(frame), nil
}

func (r *Runtime) dialForwardRelay(ctx context.Context, destination target, protocol string) (net.Conn, error) {
	r.mu.Lock()
	relay := r.relay
	r.mu.Unlock()
	if relay == nil {
		return nil, errors.New("forward relay is unavailable")
	}
	relay.mu.RLock()
	route := relay.routes[destination.lineID]
	relay.mu.RUnlock()
	if route == nil || route.Generation != destination.generation || route.Next == nil ||
		route.PreviousNodeID != "" || route.Context.Err() != nil {
		return nil, errors.New("forward relay route is unavailable")
	}
	connectionID, err := id.NewV7()
	if err != nil {
		return nil, err
	}
	requestType := "open"
	if protocol == "UDP" {
		requestType = "open_udp"
	}
	upstream, err := agentrelay.DialLine(ctx, *route.Next, agentrelay.Open{Version: 1, Type: requestType,
		LineID: destination.lineID, ConnectionID: connectionID, Generation: route.Generation,
		TargetHost: destination.host, TargetPort: destination.port, SentAt: time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(route.Context, func() { _ = upstream.Close() })
	if route.Context.Err() != nil {
		stop()
		_ = upstream.Close()
		return nil, errors.New("forward relay route was revoked")
	}
	connection := relayForwardConn{Conn: upstream, stop: stop}
	if protocol == "UDP" {
		return &relayDatagramConn{relayForwardConn: connection}, nil
	}
	return connection, nil
}
