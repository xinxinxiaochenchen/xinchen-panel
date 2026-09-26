package agentruntime

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"controlplane/internal/agentmeter"
)

type udpSession struct {
	conn     net.Conn
	client   net.Addr
	meter    MeteredConnection
	lastSeen atomic.Int64
}

func (r *Runtime) serveUDP(listener *endpoint) {
	defer r.loops.Done()
	buffer := make([]byte, 65535)
	for {
		count, client, err := listener.udp.ReadFrom(buffer)
		if err != nil {
			if listener.closing.Load() {
				return
			}
			continue
		}
		select {
		case listener.pending <- struct{}{}:
		default:
			continue
		}
		packet := append([]byte(nil), buffer[:count]...)
		r.loops.Add(1)
		go func() {
			defer r.loops.Done()
			defer func() { <-listener.pending }()
			r.relayUDPPacket(listener, client, packet)
		}()
	}
}

func (r *Runtime) relayUDPPacket(listener *endpoint, client net.Addr, packet []byte) {
	session := r.udpAssociation(listener, client)
	if session == nil {
		return
	}
	session.lastSeen.Store(time.Now().UnixNano())
	var err error
	if session.meter != nil {
		_, err = session.meter.WritePacket(session.conn, packet, agentmeter.Upload)
	} else {
		_, err = session.conn.Write(packet)
	}
	if err != nil {
		r.removeUDPAssociation(listener, client.String(), session)
	}
}

func (r *Runtime) udpAssociation(listener *endpoint, client net.Addr) *udpSession {
	key := client.String()
	listener.udpMu.Lock()
	if session := listener.sessions[key]; session != nil {
		listener.udpMu.Unlock()
		return session
	}
	destination := listener.target.Load()
	listener.udpMu.Unlock()
	if destination == nil || listener.closing.Load() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var metered MeteredConnection
	if r.options.Meter != nil {
		var err error
		metered, err = r.options.Meter.Open(ctx, "forward", destination.id, destination.revision)
		if err != nil {
			return nil
		}
	} else if r.options.RequireMetering {
		return nil
	}
	accepted := false
	defer func() {
		if !accepted && metered != nil {
			_ = metered.Close()
		}
	}()
	address, err := ResolvePublic(ctx, destination.host, r.options.Resolve)
	if err != nil {
		return nil
	}
	upstream, err := r.options.DialUDP(ctx, net.JoinHostPort(address.String(), strconv.Itoa(destination.port)))
	if err != nil {
		return nil
	}
	listener.udpMu.Lock()
	if listener.closing.Load() || listener.target.Load() != destination {
		listener.udpMu.Unlock()
		_ = upstream.Close()
		return nil
	}
	if existing := listener.sessions[key]; existing != nil {
		listener.udpMu.Unlock()
		_ = upstream.Close()
		return existing
	}
	if len(listener.sessions) >= r.options.MaxUDPAssociations {
		listener.udpMu.Unlock()
		_ = upstream.Close()
		return nil
	}
	session := &udpSession{conn: upstream, client: client, meter: metered}
	session.lastSeen.Store(time.Now().UnixNano())
	listener.sessions[key] = session
	r.loops.Add(1)
	listener.udpMu.Unlock()
	accepted = true
	go r.serveUDPReplies(listener, key, session)
	return session
}

func (r *Runtime) serveUDPReplies(listener *endpoint, key string, session *udpSession) {
	defer r.loops.Done()
	defer r.removeUDPAssociation(listener, key, session)
	buffer := make([]byte, 65535)
	for {
		idleDeadline := time.Unix(0, session.lastSeen.Load()).Add(r.options.UDPIdleTimeout)
		if !time.Now().Before(idleDeadline) {
			return
		}
		_ = session.conn.SetReadDeadline(idleDeadline)
		count, err := session.conn.Read(buffer)
		if err != nil {
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() &&
				time.Since(time.Unix(0, session.lastSeen.Load())) < r.options.UDPIdleTimeout {
				continue
			}
			return
		}
		if !time.Now().Before(time.Unix(0, session.lastSeen.Load()).Add(r.options.UDPIdleTimeout)) {
			return
		}
		listener.udpMu.Lock()
		if listener.sessions[key] != session || listener.closing.Load() {
			listener.udpMu.Unlock()
			return
		}
		if session.meter != nil {
			_, err = session.meter.WritePacket(packetWriter{listener.udp, session.client}, buffer[:count], agentmeter.Download)
		} else {
			_, err = listener.udp.WriteTo(buffer[:count], session.client)
		}
		listener.udpMu.Unlock()
		if err != nil {
			return
		}
	}
}

func (r *Runtime) removeUDPAssociation(listener *endpoint, key string, session *udpSession) {
	listener.udpMu.Lock()
	if listener.sessions[key] == session {
		delete(listener.sessions, key)
	}
	listener.udpMu.Unlock()
	_ = session.conn.Close()
	if session.meter != nil {
		_ = session.meter.Close()
	}
}

type packetWriter struct {
	conn   net.PacketConn
	client net.Addr
}

func (w packetWriter) Write(payload []byte) (int, error) { return w.conn.WriteTo(payload, w.client) }
