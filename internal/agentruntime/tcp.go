package agentruntime

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"controlplane/internal/agentmeter"
)

type tcpSession struct {
	mu       sync.Mutex
	client   net.Conn
	upstream net.Conn
	meter    MeteredConnection
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
}

func newTCPSession(client net.Conn) *tcpSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &tcpSession{client: client, ctx: ctx, cancel: cancel}
}

func (session *tcpSession) setUpstream(connection net.Conn) bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		_ = connection.Close()
		return false
	}
	session.upstream = connection
	return true
}

func (session *tcpSession) setMeter(meter MeteredConnection) bool {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		_ = meter.Close()
		return false
	}
	session.meter = meter
	session.mu.Unlock()
	return true
}

func (session *tcpSession) close() {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return
	}
	session.closed = true
	upstream := session.upstream
	meter := session.meter
	session.meter = nil
	session.mu.Unlock()
	session.cancel()
	_ = session.client.Close()
	if upstream != nil {
		_ = upstream.Close()
	}
	if meter != nil {
		// Closing a metered connection may synchronously wait for the control
		// stream to acknowledge a final settlement. Do not block Apply or the
		// stream reader while revoking a data-plane session.
		go func() { _ = meter.Close() }()
	}
}

func (listener *endpoint) addTCPSession(session *tcpSession) bool {
	listener.tcpMu.Lock()
	defer listener.tcpMu.Unlock()
	if listener.closing.Load() {
		return false
	}
	listener.tcpSessions[session] = struct{}{}
	return true
}

func (listener *endpoint) removeTCPSession(session *tcpSession) {
	listener.tcpMu.Lock()
	delete(listener.tcpSessions, session)
	listener.tcpMu.Unlock()
	session.close()
}

func (listener *endpoint) closeTCPSessions() {
	listener.tcpMu.Lock()
	sessions := listener.tcpSessions
	listener.tcpSessions = make(map[*tcpSession]struct{})
	listener.tcpMu.Unlock()
	for session := range sessions {
		session.close()
	}
}

func (listener *endpoint) swapTCPTarget(destination *target) {
	listener.tcpMu.Lock()
	previous := listener.target.Load()
	listener.target.Store(destination)
	if previous != nil && *previous == *destination {
		listener.tcpMu.Unlock()
		return
	}
	sessions := listener.tcpSessions
	listener.tcpSessions = make(map[*tcpSession]struct{})
	listener.tcpMu.Unlock()
	for session := range sessions {
		session.close()
	}
}

func (r *Runtime) serveTCP(listener *endpoint) {
	defer r.loops.Done()
	for {
		client, err := listener.tcp.Accept()
		if err != nil {
			if listener.closing.Load() {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		select {
		case listener.tcpSlots <- struct{}{}:
			session := newTCPSession(client)
			if !listener.addTCPSession(session) {
				session.close()
				<-listener.tcpSlots
				continue
			}
			r.loops.Add(1)
			go func() {
				defer r.loops.Done()
				defer func() { <-listener.tcpSlots }()
				defer listener.removeTCPSession(session)
				r.relayTCP(listener, session)
			}()
		default:
			_ = client.Close()
		}
	}
}

func (r *Runtime) relayTCP(listener *endpoint, session *tcpSession) {
	defer session.close()
	destination := listener.target.Load()
	if destination == nil {
		return
	}
	var metered MeteredConnection
	if r.options.Meter != nil {
		var err error
		metered, err = r.options.Meter.Open(session.ctx, "forward", destination.id, destination.revision)
		if err != nil {
			return
		}
		if !session.setMeter(metered) {
			return
		}
		defer metered.Close()
	} else if r.options.RequireMetering {
		return
	}
	ctx, cancel := context.WithTimeout(session.ctx, 10*time.Second)
	defer cancel()
	var upstream net.Conn
	var err error
	if destination.lineID != "" {
		upstream, err = r.dialForwardRelay(ctx, *destination, "TCP")
	} else {
		address, resolveErr := ResolvePublic(ctx, destination.host, r.options.Resolve)
		if resolveErr != nil {
			return
		}
		upstream, err = r.options.DialTCP(ctx, net.JoinHostPort(address.String(), strconv.Itoa(destination.port)))
	}
	if err != nil {
		return
	}
	if !session.setUpstream(upstream) {
		return
	}
	uploadDone := make(chan error, 1)
	downloadDone := make(chan error, 1)
	copyPayload := func(dst io.Writer, src io.Reader, direction agentmeter.Direction) error {
		if metered != nil {
			_, err := metered.Copy(dst, src, direction)
			return err
		}
		_, err := io.Copy(dst, src)
		return err
	}
	go func() {
		err := copyPayload(upstream, session.client, agentmeter.Upload)
		closeWrite(upstream)
		uploadDone <- err
	}()
	go func() {
		err := copyPayload(session.client, upstream, agentmeter.Download)
		closeWrite(session.client)
		downloadDone <- err
	}()
	select {
	case err := <-uploadDone:
		if err != nil {
			session.close()
		}
		select {
		case <-downloadDone:
		case <-time.After(r.options.TCPDrainTimeout):
			session.close()
			<-downloadDone
		}
	case err := <-downloadDone:
		if err != nil {
			session.close()
		}
		select {
		case <-uploadDone:
		case <-time.After(r.options.TCPDrainTimeout):
			session.close()
			<-uploadDone
		}
	}
}

func closeWrite(connection net.Conn) {
	if halfCloser, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = halfCloser.CloseWrite()
	} else {
		_ = connection.Close()
	}
}
