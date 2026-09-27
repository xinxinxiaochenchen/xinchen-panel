package agentruntime

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"controlplane/internal/agentrelay"
)

type RelayClientTLS func(context.Context, RelayNextHop) (*tls.Config, error)

type relayEndpoint struct {
	mu             sync.RWMutex
	listener       net.Listener
	routes         map[string]*agentrelay.Route
	configs        map[string]RelayConfig
	cancels        map[string]context.CancelFunc
	cancel         context.CancelFunc
	done           chan struct{}
	maxConnections chan struct{}
}

func newRelayEndpoint(parent context.Context, listener net.Listener, handler *agentrelay.Handler, max int) *relayEndpoint {
	ctx, cancel := context.WithCancel(parent)
	endpoint := &relayEndpoint{listener: listener, routes: map[string]*agentrelay.Route{}, configs: map[string]RelayConfig{}, cancels: map[string]context.CancelFunc{}, cancel: cancel, done: make(chan struct{}), maxConnections: make(chan struct{}, max)}
	handler.Routes = func(lineID string) (*agentrelay.Route, bool) {
		endpoint.mu.RLock()
		defer endpoint.mu.RUnlock()
		route, ok := endpoint.routes[lineID]
		return route, ok
	}
	go endpoint.serve(ctx, handler)
	return endpoint
}

func (e *relayEndpoint) serve(ctx context.Context, handler *agentrelay.Handler) {
	defer close(e.done)
	for {
		conn, err := e.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(50 * time.Millisecond)
				continue
			}
		}
		select {
		case e.maxConnections <- struct{}{}:
		case <-ctx.Done():
			_ = conn.Close()
			return
		}
		tlsConn, ok := conn.(*tls.Conn)
		if !ok {
			_ = conn.Close()
			<-e.maxConnections
			continue
		}
		go func() {
			defer func() { <-e.maxConnections }()
			_ = handler.HandleConn(ctx, tlsConn)
		}()
	}
}

func (e *relayEndpoint) replace(routes map[string]*agentrelay.Route, cancels map[string]context.CancelFunc, configs []RelayConfig) {
	e.mu.Lock()
	oldRoutes, oldCancels, oldConfigs := e.routes, e.cancels, e.configs
	nextConfigs := make(map[string]RelayConfig, len(configs))
	for _, config := range configs {
		nextConfigs[config.LineID] = cloneRelayConfig(config)
	}
	for line, route := range routes {
		if old := oldRoutes[line]; old != nil && sameRelayConfig(oldConfigs[line], nextConfigs[line]) {
			cancels[line]()
			routes[line], cancels[line] = old, oldCancels[line]
			delete(oldCancels, line)
		} else if old != nil && old.Generation == route.Generation {
			route.Window = old.Window
		}
	}
	e.routes, e.cancels, e.configs = routes, cancels, nextConfigs
	e.mu.Unlock()
	for _, cancel := range oldCancels {
		cancel()
	}
}

func sameRelayConfig(left, right RelayConfig) bool {
	if left.LineID != right.LineID || left.Generation != right.Generation || left.Role != right.Role ||
		left.PreviousNodeID != right.PreviousNodeID || !bytes.Equal(left.PreviousSecret, right.PreviousSecret) ||
		!slices.Equal(left.PreviousFingerprints, right.PreviousFingerprints) || (left.Next == nil) != (right.Next == nil) {
		return false
	}
	if left.Next == nil {
		return true
	}
	return left.Next.NodeID == right.Next.NodeID && left.Next.Address == right.Next.Address &&
		left.Next.Port == right.Next.Port && bytes.Equal(left.Next.Secret, right.Next.Secret) &&
		slices.Equal(left.Next.Fingerprints, right.Next.Fingerprints)
}

func cloneRelayConfig(config RelayConfig) RelayConfig {
	config.PreviousSecret = bytes.Clone(config.PreviousSecret)
	config.PreviousFingerprints = slices.Clone(config.PreviousFingerprints)
	if config.Next != nil {
		next := *config.Next
		next.Secret = bytes.Clone(next.Secret)
		next.Fingerprints = slices.Clone(next.Fingerprints)
		config.Next = &next
	}
	return config
}

func (e *relayEndpoint) close() {
	e.cancel()
	_ = e.listener.Close()
	e.mu.Lock()
	cancels := e.cancels
	e.routes, e.cancels, e.configs = map[string]*agentrelay.Route{}, map[string]context.CancelFunc{}, map[string]RelayConfig{}
	e.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	<-e.done
}

// buildRelayRoutes turns an applied snapshot into immutable handler routes.
func buildRelayRoutes(parent context.Context, configs []RelayConfig, clientTLS RelayClientTLS) (map[string]*agentrelay.Route, map[string]context.CancelFunc, error) {
	if err := ValidateRelayConfig(configs); err != nil {
		return nil, nil, err
	}
	routes := make(map[string]*agentrelay.Route, len(configs))
	cancels := make(map[string]context.CancelFunc, len(configs))
	abort := func(err error) (map[string]*agentrelay.Route, map[string]context.CancelFunc, error) {
		for _, cancel := range cancels {
			cancel()
		}
		return nil, nil, err
	}
	for _, config := range configs {
		routeCtx, cancel := context.WithCancel(parent)
		route := &agentrelay.Route{LineID: config.LineID, Generation: config.Generation, PreviousNodeID: config.PreviousNodeID, PreviousSecret: append([]byte(nil), config.PreviousSecret...), PreviousFingerprints: append([]string(nil), config.PreviousFingerprints...), Window: agentrelay.NewReplayWindow(4096), Context: routeCtx}
		if config.Next != nil {
			if clientTLS == nil {
				cancel()
				return abort(errors.New("relay client TLS builder is not configured"))
			}
			tlsConfig, err := clientTLS(routeCtx, *config.Next)
			if err != nil {
				cancel()
				return abort(fmt.Errorf("build relay next-hop TLS: %w", err))
			}
			route.Next = &agentrelay.NextHop{Address: config.Next.Address, TLSConfig: tlsConfig, Secret: append([]byte(nil), config.Next.Secret...)}
		}
		if _, exists := routes[config.LineID]; exists {
			cancel()
			return abort(errors.New("duplicate relay line route"))
		}
		routes[config.LineID], cancels[config.LineID] = route, cancel
	}
	return routes, cancels, nil
}

func (r *Runtime) applyRelay(configs []RelayConfig) error {
	if len(configs) == 0 {
		if r.relay != nil {
			r.relay.close()
			r.relay = nil
		}
		return nil
	}
	if r.options.RelayPort < 1024 || r.options.RelayPort > 65535 || r.options.RelayTLSConfig == nil {
		return errors.New("relay listener requires a valid port and TLS configuration")
	}
	if len(r.options.RelayTLSConfig.Certificates) == 0 && r.options.RelayTLSConfig.GetCertificate == nil {
		return errors.New("relay listener TLS certificate is not configured")
	}
	routes, cancels, err := buildRelayRoutes(context.Background(), configs, r.options.RelayClientTLS)
	if err != nil {
		return err
	}
	if r.relay == nil {
		listener, err := net.Listen("tcp", net.JoinHostPort(r.options.BindHost, fmt.Sprint(r.options.RelayPort)))
		if err != nil {
			for _, cancel := range cancels {
				cancel()
			}
			return fmt.Errorf("bind relay port %d: %w", r.options.RelayPort, err)
		}
		dialRelay := r.options.RelayDial
		if dialRelay == nil {
			dialRelay = r.options.DialTCP
		}
		handler := &agentrelay.Handler{
			Resolve: func(resolveCtx context.Context, host string) (netip.Addr, error) {
				return ResolvePublic(resolveCtx, host, r.options.Resolve)
			},
			DialTarget:         r.options.DialTCP,
			DialDatagramTarget: r.options.DialUDP,
			DialRelay:          dialRelay,
		}
		r.relay = newRelayEndpoint(context.Background(), tls.NewListener(listener, r.options.RelayTLSConfig.Clone()), handler, r.options.MaxTCPConnections)
	}
	r.relay.replace(routes, cancels, configs)
	return nil
}
