package agentruntime

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

type tcpSession struct {
	mu       sync.Mutex
	client   net.Conn
	upstream net.Conn
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

func (session *tcpSession) close() {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return
	}
	session.closed = true
	upstream := session.upstream
	session.mu.Unlock()
	session.cancel()
	_ = session.client.Close()
	if upstream != nil {
		_ = upstream.Close()
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
	ctx, cancel := context.WithTimeout(session.ctx, 10*time.Second)
	defer cancel()
	address, err := ResolvePublic(ctx, destination.host, r.options.Resolve)
	if err != nil {
		return
	}
	upstream, err := r.options.DialTCP(ctx, net.JoinHostPort(address.String(), strconv.Itoa(destination.port)))
	if err != nil {
		return
	}
	if !session.setUpstream(upstream) {
		return
	}
	uploadDone := make(chan struct{})
	downloadDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, session.client)
		closeWrite(upstream)
		close(uploadDone)
	}()
	go func() {
		_, _ = io.Copy(session.client, upstream)
		closeWrite(session.client)
		close(downloadDone)
	}()
	select {
	case <-uploadDone:
		select {
		case <-downloadDone:
		case <-time.After(r.options.TCPDrainTimeout):
			session.close()
			<-downloadDone
		}
	case <-downloadDone:
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
