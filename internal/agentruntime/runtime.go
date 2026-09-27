package agentruntime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"maps"
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
	RequireMetering      bool
	Meter                TrafficMeter
	ProxyTLSConfig       *tls.Config
	Resolve              Resolver
	DialTCP              DialFunc
	DialUDP              DialFunc
	MaxTCPConnections    int
	MaxUDPAssociations   int
	MaxPendingUDPPackets int
	UDPIdleTimeout       time.Duration
	TCPDrainTimeout      time.Duration
	RelayPort            int
	RelayTLSConfig       *tls.Config
	RelayClientTLS       RelayClientTLS
	RelayDial            DialFunc
}

type Runtime struct {
	mu        sync.Mutex
	options   Options
	listeners map[listenerKey]*endpoint
	proxies   map[int]*proxyEndpoint
	revision  uint64
	closed    bool
	closeDone chan struct{}
	loops     sync.WaitGroup
	relay     *relayEndpoint
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
	return &Runtime{options: options, listeners: make(map[listenerKey]*endpoint),
		proxies: make(map[int]*proxyEndpoint), closeDone: make(chan struct{})}
}

// SetTrafficMeter attaches the authenticated stream-backed meter before any
// traffic snapshot is applied. Existing listeners keep their current meter;
// production callers set it immediately after creating a Runtime.
func (r *Runtime) SetTrafficMeter(meter TrafficMeter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("forward runtime is closed")
	}
	if meter == nil && r.options.RequireMetering {
		return errors.New("traffic metering is required")
	}
	r.options.Meter = meter
	return nil
}

// HandleUsageAck forwards a persisted usage acknowledgement to a stream-backed
// meter. It is optional so test runtimes and non-metered deployments remain
// small; production meters use it to settle connections that already stopped.
func (r *Runtime) HandleUsageAck(batchID string) error {
	r.mu.Lock()
	meter := r.options.Meter
	r.mu.Unlock()
	if handler, ok := meter.(interface{ HandleUsageAck(string) error }); ok {
		return handler.HandleUsageAck(batchID)
	}
	return nil
}

func (r *Runtime) SettleRecovered(ctx context.Context) error {
	r.mu.Lock()
	meter := r.options.Meter
	r.mu.Unlock()
	if handler, ok := meter.(interface{ SettleRecovered(context.Context) error }); ok {
		return handler.SettleRecovered(ctx)
	}
	return nil
}

func (r *Runtime) Apply(ctx context.Context, snapshot Snapshot) error {
	wanted, err := ValidateSnapshot(snapshot)
	if err != nil {
		return err
	}
	if r.options.RequireMetering && r.options.Meter == nil && (len(wanted) > 0 || len(snapshot.ProxyConfig) > 0) {
		return errors.New("traffic metering is not configured")
	}
	for key, destination := range wanted {
		destination.revision = snapshot.Revision
		wanted[key] = destination
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("forward runtime is closed")
	}
	if snapshot.Revision <= r.revision {
		return fmt.Errorf("stale forward snapshot revision %d", snapshot.Revision)
	}
	wantedProxies := make(map[int]map[string]ProxyAccess)
	for _, access := range snapshot.ProxyConfig {
		if wantedProxies[access.IngressPort] == nil {
			wantedProxies[access.IngressPort] = make(map[string]ProxyAccess)
		}
		wantedProxies[access.IngressPort][access.CredentialHash] = access
	}
	if len(wantedProxies) > 0 && (r.options.ProxyTLSConfig == nil ||
		len(r.options.ProxyTLSConfig.Certificates) == 0) {
		return errors.New("proxy TLS certificate is not configured")
	}
	staged := make(map[listenerKey]*endpoint)
	stagedProxies := make(map[int]*proxyEndpoint)
	defer func() {
		for _, listener := range staged {
			listener.close()
		}
		for _, listener := range stagedProxies {
			listener.close()
		}
	}()
	listen := net.ListenConfig{}
	for port, accesses := range wantedProxies {
		if _, exists := r.proxies[port]; exists {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		address := net.JoinHostPort(r.options.BindHost, strconv.Itoa(port))
		raw, err := listen.Listen(ctx, "tcp", address)
		if err != nil {
			return fmt.Errorf("bind proxy TLS port %d: %w", port, err)
		}
		stagedProxies[port] = newProxyEndpoint(tls.NewListener(raw, r.options.ProxyTLSConfig), accesses, snapshot.Revision)
	}
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
	if err := r.applyRelay(snapshot.RelayConfig); err != nil {
		return err
	}
	for port, listener := range r.proxies {
		accesses, keep := wantedProxies[port]
		if !keep {
			listener.close()
			delete(r.proxies, port)
			continue
		}
		listener.mu.Lock()
		listener.revision = snapshot.Revision
		if !maps.Equal(listener.accesses, accesses) {
			listener.accesses = accesses
			for session, previous := range listener.sessions {
				if previous.ID == "" {
					continue
				}
				current, allowed := accesses[previous.CredentialHash]
				if !allowed || current != previous {
					session.close()
				}
			}
		}
		listener.mu.Unlock()
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
	for port, listener := range stagedProxies {
		r.proxies[port] = listener
		r.loops.Add(1)
		go r.serveProxy(listener)
		delete(stagedProxies, port)
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
	for port, listener := range r.proxies {
		listener.close()
		delete(r.proxies, port)
	}
	if r.relay != nil {
		r.relay.close()
		r.relay = nil
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
		if session.meter != nil {
			_ = session.meter.Close()
		}
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
		if session.meter != nil {
			_ = session.meter.Close()
		}
	}
}
