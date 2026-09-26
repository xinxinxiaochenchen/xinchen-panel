package agentruntime

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// proxyEndpoint owns one Trojan TLS listener and its credential set. It shares
// the Runtime transaction boundary with forward listeners so a bind failure
// cannot partially apply a configuration.
type proxyEndpoint struct {
	listener net.Listener
	mu       sync.Mutex
	accesses map[string]ProxyAccess
	sessions map[*tcpSession]ProxyAccess
	closed   bool
}

func newProxyEndpoint(listener net.Listener, accesses map[string]ProxyAccess) *proxyEndpoint {
	return &proxyEndpoint{listener: listener, accesses: accesses, sessions: make(map[*tcpSession]ProxyAccess)}
}

func (p *proxyEndpoint) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	_ = p.listener.Close()
	for session := range p.sessions {
		session.close()
	}
}

func (r *Runtime) serveProxy(p *proxyEndpoint) {
	defer r.loops.Done()
	slots := make(chan struct{}, r.options.MaxTCPConnections)
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			p.mu.Lock()
			closed := p.closed
			p.mu.Unlock()
			if closed {
				return
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		session := newTCPSession(conn)
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			session.close()
			<-slots
			continue
		}
		p.sessions[session] = ProxyAccess{}
		p.mu.Unlock()
		r.loops.Add(1)
		go func() {
			defer r.loops.Done()
			defer func() { <-slots; p.mu.Lock(); delete(p.sessions, session); p.mu.Unlock(); session.close() }()
			r.relayProxy(p, session)
		}()
	}
}

func (r *Runtime) relayProxy(p *proxyEndpoint, session *tcpSession) {
	_ = session.client.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReaderSize(session.client, 512)
	hash, host, port, err := readTrojanConnect(reader)
	if err != nil {
		return
	}
	p.mu.Lock()
	access, allowed := p.accesses[hash]
	allowed = allowed && !p.closed && access.ExpiresAt.After(time.Now())
	if !allowed {
		p.mu.Unlock()
		return
	}
	p.sessions[session] = access
	// Capture the credential generation while holding the same mutex used by
	// Apply: a rotation between authentication and dialing closes this session.
	expiry := time.AfterFunc(time.Until(access.ExpiresAt), session.close)
	p.mu.Unlock()
	defer expiry.Stop()
	ctx, cancel := context.WithTimeout(session.ctx, 10*time.Second)
	address, err := ResolvePublic(ctx, host, r.options.Resolve)
	if err != nil {
		cancel()
		return
	}
	upstream, err := r.options.DialTCP(ctx, net.JoinHostPort(address.String(), strconv.Itoa(port)))
	cancel()
	if err != nil || !session.setUpstream(upstream) {
		return
	}
	_ = session.client.SetDeadline(time.Time{})
	relayProxyStreams(session, reader, r.options.TCPDrainTimeout)
}

func readTrojanConnect(reader *bufio.Reader) (string, string, int, error) {
	var auth [58]byte
	if _, err := io.ReadFull(reader, auth[:]); err != nil {
		return "", "", 0, err
	}
	if string(auth[56:]) != "\r\n" || !trojanHashPattern.Match(auth[:56]) {
		return "", "", 0, errors.New("invalid Trojan authentication")
	}
	var command [2]byte
	if _, err := io.ReadFull(reader, command[:]); err != nil {
		return "", "", 0, err
	}
	if command[0] != 1 {
		return "", "", 0, errors.New("only Trojan TCP CONNECT is supported")
	}
	var host string
	switch command[1] {
	case 1, 4:
		length := 4
		if command[1] == 4 {
			length = 16
		}
		address := make([]byte, length)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", "", 0, err
		}
		host = net.IP(address).String()
	case 3:
		length, err := reader.ReadByte()
		if err != nil || length == 0 {
			return "", "", 0, errors.New("invalid Trojan hostname")
		}
		address := make([]byte, int(length))
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", "", 0, err
		}
		host = string(address)
	default:
		return "", "", 0, errors.New("invalid Trojan address type")
	}
	var tail [4]byte
	if _, err := io.ReadFull(reader, tail[:]); err != nil {
		return "", "", 0, err
	}
	port := int(binary.BigEndian.Uint16(tail[:2]))
	if port == 0 || string(tail[2:]) != "\r\n" {
		return "", "", 0, errors.New("invalid Trojan target port or delimiter")
	}
	return string(auth[:56]), host, port, nil
}

func relayProxyStreams(session *tcpSession, reader io.Reader, drain time.Duration) {
	upload := make(chan struct{})
	download := make(chan struct{})
	go func() { _, _ = io.Copy(session.upstream, reader); closeWrite(session.upstream); close(upload) }()
	go func() { _, _ = io.Copy(session.client, session.upstream); closeWrite(session.client); close(download) }()
	select {
	case <-upload:
	case <-download:
	}
	timer := time.NewTimer(drain)
	defer timer.Stop()
	select {
	case <-upload:
	case <-timer.C:
		session.close()
	}
	select {
	case <-download:
	case <-timer.C:
		session.close()
	}
	<-upload
	<-download
}
