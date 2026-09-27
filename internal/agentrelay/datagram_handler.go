package agentrelay

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"controlplane/internal/forward"
)

const datagramIdleTimeout = 60 * time.Second

func (handler *Handler) handleDatagramOpen(ctx context.Context, client *tls.Conn, state tls.ConnectionState, peerID string, request Open) error {
	reject := func(code string) error {
		_ = WriteOpenResponse(client, OpenResponse{Version: 1, Type: "open_err", ConnectionID: request.ConnectionID, ErrorCode: code})
		return errors.New("relay datagram open rejected: " + code)
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
	stopRoute := context.AfterFunc(route.Context, func() { closeImmediately(client) })
	defer stopRoute()
	if !route.ExpiresAt.IsZero() {
		expiry := time.AfterFunc(time.Until(route.ExpiresAt), func() { closeImmediately(client) })
		defer expiry.Stop()
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if route.Next != nil {
		upstream, err := dialLine(dialCtx, *route.Next, request, handler.DialRelay)
		if err != nil {
			return reject("UPSTREAM_UNAVAILABLE")
		}
		defer closeImmediately(upstream)
		stopUpstream := context.AfterFunc(ctx, func() { closeImmediately(upstream) })
		defer stopUpstream()
		if err := WriteOpenResponse(client, OpenResponse{Version: 1, Type: "open_ok", ConnectionID: request.ConnectionID}); err != nil {
			return errors.New("relay datagram response write failed")
		}
		_ = client.SetDeadline(time.Time{})
		copyFramedDatagrams(client, upstream)
		return nil
	}
	if handler.Resolve == nil || handler.DialDatagramTarget == nil {
		return reject("UPSTREAM_UNAVAILABLE")
	}
	address, err := handler.Resolve(dialCtx, request.TargetHost)
	if err != nil || !forward.PublicIP(address) {
		return reject("TARGET_REJECTED")
	}
	upstream, err := handler.DialDatagramTarget(dialCtx, net.JoinHostPort(address.Unmap().String(), strconv.Itoa(request.TargetPort)))
	if err != nil || upstream == nil {
		return reject("UPSTREAM_UNAVAILABLE")
	}
	defer closeImmediately(upstream)
	stopUpstream := context.AfterFunc(ctx, func() { closeImmediately(upstream) })
	defer stopUpstream()
	if ctx.Err() != nil || route.Context.Err() != nil {
		return reject("UNAUTHORIZED")
	}
	if err := WriteOpenResponse(client, OpenResponse{Version: 1, Type: "open_ok", ConnectionID: request.ConnectionID}); err != nil {
		return errors.New("relay datagram response write failed")
	}
	_ = client.SetDeadline(time.Time{})
	return copyDatagrams(client, upstream)
}

func copyFramedDatagrams(client net.Conn, upstream net.Conn) error {
	return copyFramedDatagramsWithIdle(client, upstream, datagramIdleTimeout)
}

func copyFramedDatagramsWithIdle(client net.Conn, upstream net.Conn, idle time.Duration) error {
	lastSeen := &atomic.Int64{}
	lastSeen.Store(time.Now().UnixNano())
	done := make(chan error, 2)
	forward := func(dst, src net.Conn) {
		for {
			payload, err := readDatagramUntilIdle(src, lastSeen, idle)
			if err != nil {
				done <- err
				return
			}
			lastSeen.Store(time.Now().UnixNano())
			if err := WriteDatagram(dst, payload); err != nil {
				done <- err
				return
			}
		}
	}
	go forward(upstream, client)
	go forward(client, upstream)
	err := <-done
	closeImmediately(client)
	closeImmediately(upstream)
	<-done
	return err
}

func copyDatagrams(client net.Conn, upstream net.Conn) error {
	return copyDatagramsWithIdle(client, upstream, datagramIdleTimeout)
}

func copyDatagramsWithIdle(client net.Conn, upstream net.Conn, idle time.Duration) error {
	var lastSeen atomic.Int64
	lastSeen.Store(time.Now().UnixNano())
	touch := func() { lastSeen.Store(time.Now().UnixNano()) }
	done := make(chan error, 2)
	go func() {
		for {
			payload, err := readDatagramUntilIdle(client, &lastSeen, idle)
			if err != nil {
				done <- err
				return
			}
			touch()
			written, err := upstream.Write(payload)
			if err != nil || written != len(payload) {
				done <- io.ErrShortWrite
				return
			}
		}
	}()
	go func() {
		buffer := make([]byte, MaxDatagramPayloadBytes+1)
		for {
			deadline := time.Unix(0, lastSeen.Load()).Add(idle)
			_ = upstream.SetReadDeadline(deadline)
			count, err := upstream.Read(buffer)
			if err != nil {
				if timeoutStillActive(err, &lastSeen, idle) {
					continue
				}
				done <- err
				return
			}
			if count > MaxDatagramPayloadBytes {
				done <- errors.New("upstream datagram exceeds relay limit")
				return
			}
			touch()
			if err := WriteDatagram(client, buffer[:count]); err != nil {
				done <- err
				return
			}
		}
	}()
	err := <-done
	closeImmediately(client)
	closeImmediately(upstream)
	<-done
	return err
}

func readDatagramUntilIdle(conn net.Conn, lastSeen *atomic.Int64, idle time.Duration) ([]byte, error) {
	readFull := func(buffer []byte) error {
		for len(buffer) > 0 {
			_ = conn.SetReadDeadline(time.Unix(0, lastSeen.Load()).Add(idle))
			count, err := conn.Read(buffer)
			buffer = buffer[count:]
			if err != nil {
				if timeoutStillActive(err, lastSeen, idle) {
					continue
				}
				return err
			}
			if count == 0 {
				return io.ErrNoProgress
			}
		}
		return nil
	}
	var header [4]byte
	if err := readFull(header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length > MaxDatagramPayloadBytes {
		return nil, errors.New("datagram payload is too large")
	}
	payload := make([]byte, int(length))
	if err := readFull(payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func timeoutStillActive(err error, lastSeen *atomic.Int64, idle time.Duration) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout() &&
		time.Since(time.Unix(0, lastSeen.Load())) < idle
}
