package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type DialFunc func(context.Context, string) (net.Conn, error)

type Options struct {
	BindHost             string
	Resolve              Resolver
	DialTCP              DialFunc
	DialUDP              DialFunc
	MaxTCPConnections    int
	MaxUDPAssociations   int
	MaxPendingUDPPackets int
	UDPIdleTimeout       time.Duration
	TCPDrainTimeout      time.Duration
}

type Runtime struct {
	mu        sync.Mutex
	options   Options
	listeners map[listenerKey]*endpoint
	revision  uint64
	closed    bool
	closeDone chan struct{}
	loops     sync.WaitGroup
}

type endpoint struct {
	key         listenerKey
	tcp         net.Listener
	tcpSlots    chan struct{}
	tcpMu       sync.Mutex
	tcpSessions map[*tcpSession]struct{}
	udp         net.PacketConn
	target      atomic.Pointer[target]
	closing     atomic.Bool
	udpMu       sync.Mutex
	sessions    map[string]*udpSession
	pending     chan struct{}
}

func New(options Options) *Runtime {
	if options.BindHost == "" {
		options.BindHost = "0.0.0.0"
	}
	if options.Resolve == nil {
		options.Resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	if options.DialTCP == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		options.DialTCP = func(ctx context.Context, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", address)
		}
	}
	if options.DialUDP == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		options.DialUDP = func(ctx context.Context, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "udp", address)
		}
	}
	if options.MaxUDPAssociations <= 0 {
		options.MaxUDPAssociations = 1024
	}
	if options.MaxTCPConnections <= 0 {
		options.MaxTCPConnections = 1024
	}
	if options.MaxPendingUDPPackets <= 0 {
		options.MaxPendingUDPPackets = 32
	}
	if options.UDPIdleTimeout <= 0 {
		options.UDPIdleTimeout = 60 * time.Second
	}
	if options.TCPDrainTimeout <= 0 {
		options.TCPDrainTimeout = 30 * time.Second
	}
	return &Runtime{options: options, listeners: make(map[listenerKey]*endpoint), closeDone: make(chan struct{})}
}

func (r *Runtime) Apply(ctx context.Context, snapshot Snapshot) error {
	wanted, err := ValidateSnapshot(snapshot)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("forward runtime is closed")
	}
	if snapshot.Revision <= r.revision {
		return fmt.Errorf("stale forward snapshot revision %d", snapshot.Revision)
	}
	staged := make(map[listenerKey]*endpoint)
	defer func() {
		for _, listener := range staged {
			listener.close()
		}
	}()
	listen := net.ListenConfig{}
	for key, destination := range wanted {
		if _, exists := r.listeners[key]; exists {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		address := net.JoinHostPort(r.options.BindHost, strconv.Itoa(key.port))
		endpoint := &endpoint{key: key, sessions: make(map[string]*udpSession),
			pending:     make(chan struct{}, r.options.MaxPendingUDPPackets),
			tcpSlots:    make(chan struct{}, r.options.MaxTCPConnections),
			tcpSessions: make(map[*tcpSession]struct{})}
		value := destination
		endpoint.target.Store(&value)
		if key.protocol == "TCP" {
			endpoint.tcp, err = listen.Listen(ctx, "tcp", address)
		} else {
			endpoint.udp, err = listen.ListenPacket(ctx, "udp", address)
		}
		if err != nil {
			return fmt.Errorf("bind %s port %d: %w", key.protocol, key.port, err)
		}
		staged[key] = endpoint
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for key, listener := range r.listeners {
		destination, keep := wanted[key]
		if !keep {
			listener.close()
			delete(r.listeners, key)
			continue
		}
		value := destination
		if key.protocol == "UDP" {
			listener.swapUDPTarget(&value)
		} else {
			listener.swapTCPTarget(&value)
		}
	}
	for key, listener := range staged {
		r.listeners[key] = listener
		r.loops.Add(1)
		if key.protocol == "TCP" {
			go r.serveTCP(listener)
		} else {
			go r.serveUDP(listener)
		}
		delete(staged, key)
	}
	r.revision = snapshot.Revision
	return nil
}

func (r *Runtime) Revision() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.revision
}

func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		<-r.closeDone
		return nil
	}
	r.closed = true
	for key, listener := range r.listeners {
		listener.close()
		delete(r.listeners, key)
	}
	r.mu.Unlock()
	r.loops.Wait()
	close(r.closeDone)
	return nil
}

func (e *endpoint) close() {
	if e.closing.Swap(true) {
		return
	}
	if e.tcp != nil {
		_ = e.tcp.Close()
	}
	if e.udp != nil {
		_ = e.udp.Close()
	}
	e.closeTCPSessions()
	e.udpMu.Lock()
	sessions := e.sessions
	e.sessions = make(map[string]*udpSession)
	e.udpMu.Unlock()
	for _, session := range sessions {
		_ = session.conn.Close()
	}
}

func (e *endpoint) swapUDPTarget(destination *target) {
	e.udpMu.Lock()
	previous := e.target.Load()
	e.target.Store(destination)
	if previous != nil && *previous == *destination {
		e.udpMu.Unlock()
		return
	}
	sessions := e.sessions
	e.sessions = make(map[string]*udpSession)
	e.udpMu.Unlock()
	for _, session := range sessions {
		_ = session.conn.Close()
	}
}
