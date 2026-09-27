package agentrelay

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"time"

	"controlplane/internal/agentidentity"
	"controlplane/internal/forward"
)

// Route is immutable except for its replay window. The configuration owner
// must retain one instance per applied line generation and cancel Context
// when the route is removed or replaced. A request must never create a new
// replay window from serialized configuration.
type Route struct {
	LineID               string
	Generation           uint64
	PreviousNodeID       string
	PreviousSecret       []byte
	PreviousFingerprints []string
	Window               *ReplayWindow
	Context              context.Context
	ExpiresAt            time.Time
	Next                 *NextHop
}

type Handler struct {
	Routes     func(lineID string) (*Route, bool)
	Resolve    func(context.Context, string) (netip.Addr, error)
	DialTarget func(context.Context, string) (net.Conn, error)
	DialRelay  func(context.Context, string) (net.Conn, error)
}

// HandleConn owns an already accepted TLS connection. TLS must use
// ServerTLSConfig; this method also checks its verified peer chain and Agent
// identity. Listener limits and source rate limiting belong to the caller.
func (handler *Handler) HandleConn(parent context.Context, client *tls.Conn) error {
	defer closeImmediately(client)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stopParent := context.AfterFunc(ctx, func() { closeImmediately(client) })
	defer stopParent()
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	if err := client.HandshakeContext(ctx); err != nil {
		return errors.New("relay TLS handshake failed")
	}
	state := client.ConnectionState()
	if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) != 1 || state.Version != tls.VersionTLS13 {
		return errors.New("relay peer is not verified")
	}
	peerID, err := agentidentity.CertificateNodeID(state.PeerCertificates[0])
	if err != nil {
		return errors.New("relay peer identity is invalid")
	}
	request, err := ReadOpen(client)
	if err != nil {
		return errors.New("relay open frame is invalid")
	}
	reject := func(code string) error {
		_ = WriteOpenResponse(client, OpenResponse{Version: 1, Type: "open_err", ConnectionID: request.ConnectionID, ErrorCode: code})
		return errors.New("relay open rejected: " + code)
	}
	if handler.Routes == nil {
		return reject("UPSTREAM_UNAVAILABLE")
	}
	route, ok := handler.Routes(request.LineID)
	if !ok || route == nil || route.PreviousNodeID != peerID || route.Context == nil ||
		route.Context.Err() != nil || !route.ExpiresAt.IsZero() && !route.ExpiresAt.After(time.Now()) {
		return reject("UNAUTHORIZED")
	}
	if len(route.PreviousFingerprints) > 0 {
		fingerprint := sha256.Sum256(state.PeerCertificates[0].Raw)
		allowed := false
		for _, value := range route.PreviousFingerprints {
			if value == hex.EncodeToString(fingerprint[:]) {
				allowed = true
				break
			}
		}
		if !allowed {
			return reject("UNAUTHORIZED")
		}
	}
	if err := VerifyOpen(request, route.PreviousSecret, route.LineID, route.Generation, route.Window, time.Now()); err != nil {
		return reject("UNAUTHORIZED")
	}
	stopRoute := context.AfterFunc(route.Context, cancel)
	defer stopRoute()
	var expires *time.Timer
	if !route.ExpiresAt.IsZero() {
		expires = time.AfterFunc(time.Until(route.ExpiresAt), cancel)
		defer expires.Stop()
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()
	var upstream net.Conn
	if route.Next != nil {
		upstream, err = dialLine(dialCtx, *route.Next, request, handler.DialRelay)
		if err != nil {
			return reject("UPSTREAM_UNAVAILABLE")
		}
	} else {
		if handler.Resolve == nil || handler.DialTarget == nil {
			return reject("UPSTREAM_UNAVAILABLE")
		}
		address, resolveErr := handler.Resolve(dialCtx, request.TargetHost)
		if resolveErr != nil || !forward.PublicIP(address) {
			return reject("TARGET_REJECTED")
		}
		upstream, err = handler.DialTarget(dialCtx, net.JoinHostPort(address.Unmap().String(), strconv.Itoa(request.TargetPort)))
		if err != nil || upstream == nil {
			return reject("UPSTREAM_UNAVAILABLE")
		}
	}
	defer closeImmediately(upstream)
	stopUpstream := context.AfterFunc(ctx, func() { closeImmediately(upstream) })
	defer stopUpstream()
	if ctx.Err() != nil {
		return reject("UNAUTHORIZED")
	}
	if err := WriteOpenResponse(client, OpenResponse{Version: 1, Type: "open_ok", ConnectionID: request.ConnectionID}); err != nil {
		return errors.New("relay response write failed")
	}
	_ = client.SetDeadline(time.Time{})
	copyStreams(client, upstream)
	return nil
}
