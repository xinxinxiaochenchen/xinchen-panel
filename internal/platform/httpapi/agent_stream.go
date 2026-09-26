package httpapi

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"controlplane/internal/agentidentity"
	"controlplane/internal/agentproto"
	"controlplane/internal/orchestration"
	"controlplane/internal/platform/id"
	"github.com/coder/websocket"
)

type AgentCertificateAuthenticator interface {
	AuthenticateCertificate(context.Context, *x509.Certificate) (string, error)
}

type AgentStreamRevisions interface {
	Desired(context.Context, string) (orchestration.DesiredRevision, error)
	RecordResult(context.Context, string, int64, string, string, string, string) error
}

type AgentStreamPresence interface {
	MarkOnline(context.Context, string, string) error
	RecordHeartbeat(context.Context, string, agentproto.Heartbeat) error
	MarkOffline(context.Context, string) error
}

type AgentStreamHandler struct {
	parent       context.Context
	logger       *slog.Logger
	auth         AgentCertificateAuthenticator
	revisions    AgentStreamRevisions
	presence     AgentStreamPresence
	mu           sync.Mutex
	active       map[string]*websocket.Conn
	pollEvery    time.Duration
	recheckEvery time.Duration
}

func NewAgentStreamHandler(parent context.Context, logger *slog.Logger, auth AgentCertificateAuthenticator,
	revisions AgentStreamRevisions, presence AgentStreamPresence) *AgentStreamHandler {
	return &AgentStreamHandler{parent: parent, logger: logger, auth: auth, revisions: revisions,
		presence: presence, active: make(map[string]*websocket.Conn), pollEvery: 3 * time.Second,
		recheckEvery: 15 * time.Second}
}

func (s *AgentStreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) != 1 {
		WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Agent client certificate required")
		return
	}
	certificate := r.TLS.PeerCertificates[0]
	certNodeID, err := agentidentity.CertificateNodeID(certificate)
	if err != nil {
		WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Agent client certificate denied")
		return
	}
	authCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	nodeID, err := s.auth.AuthenticateCertificate(authCtx, certificate)
	cancel()
	if err != nil || nodeID != certNodeID {
		status := http.StatusUnauthorized
		if err != nil && !errors.Is(err, agentidentity.ErrAgentUnauthorized) {
			status = http.StatusServiceUnavailable
		}
		WriteError(w, r, status, "UNAUTHENTICATED", "Agent client certificate denied")
		return
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(agentproto.MaxFrameBytes)
	parent := s.parent
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := context.WithCancel(parent)
	defer stop()
	if err := s.serve(ctx, connection, nodeID, certificate); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		s.logger.Warn("Agent stream ended", "node_id", nodeID, "error", err)
	}
}

type agentIncoming struct {
	envelope agentproto.Envelope
	err      error
}

func (s *AgentStreamHandler) serve(ctx context.Context, connection *websocket.Conn, nodeID string, certificate *x509.Certificate) error {
	helloCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	messageType, frame, err := connection.Read(helloCtx)
	cancel()
	if err != nil {
		return err
	}
	helloMessage, err := agentproto.Decode(frame)
	if err != nil || messageType != websocket.MessageText || helloMessage.Type != agentproto.TypeHello ||
		helloMessage.NodeID != nodeID || !freshAgentMessage(helloMessage.SentAt) {
		return errors.New("invalid Agent hello")
	}
	hello, err := agentproto.DecodeHello(helloMessage.Payload)
	if err != nil {
		return err
	}
	if err := s.presence.MarkOnline(ctx, nodeID, hello.AgentVersion); err != nil {
		return err
	}
	s.mu.Lock()
	previous := s.active[nodeID]
	s.active[nodeID] = connection
	s.mu.Unlock()
	if previous != nil {
		previous.CloseNow()
	}
	defer func() {
		s.mu.Lock()
		current := s.active[nodeID] == connection
		if current {
			delete(s.active, nodeID)
		}
		s.mu.Unlock()
		if current {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cleanupCancel()
			if err := s.presence.MarkOffline(cleanupCtx, nodeID); err != nil {
				s.logger.Warn("mark Agent offline failed", "node_id", nodeID, "error", err)
			}
		}
	}()
	appliedRevision := hello.AppliedRevision
	lastSent := int64(0)
	if err := s.sendDesired(ctx, connection, nodeID, appliedRevision, &lastSent); err != nil {
		return err
	}
	incoming := make(chan agentIncoming, 1)
	go func() {
		for {
			readCtx, readCancel := context.WithTimeout(ctx, 50*time.Second)
			kind, raw, readErr := connection.Read(readCtx)
			readCancel()
			var item agentIncoming
			if readErr != nil {
				item.err = readErr
			} else if kind != websocket.MessageText {
				item.err = errors.New("Agent sent non-text frame")
			} else {
				item.envelope, item.err = agentproto.Decode(raw)
			}
			select {
			case incoming <- item:
			case <-ctx.Done():
				return
			}
			if item.err != nil {
				return
			}
		}
	}()
	poll := time.NewTicker(s.pollEvery)
	defer poll.Stop()
	recheck := time.NewTicker(s.recheckEvery)
	defer recheck.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case item := <-incoming:
			if item.err != nil {
				return item.err
			}
			envelope := item.envelope
			if envelope.NodeID != nodeID || !freshAgentMessage(envelope.SentAt) {
				return errors.New("Agent stream identity or timestamp mismatch")
			}
			switch envelope.Type {
			case agentproto.TypeHeartbeat:
				value, err := agentproto.DecodeHeartbeat(envelope.Payload)
				if err != nil {
					return err
				}
				if err := s.presence.RecordHeartbeat(ctx, nodeID, value); err != nil {
					return err
				}
			case agentproto.TypeConfigResult:
				value, err := agentproto.DecodeConfigResult(envelope.Payload)
				if err != nil {
					return err
				}
				if err := s.revisions.RecordResult(ctx, nodeID, value.Revision, value.SHA256, value.Status, value.ErrorCode, value.ErrorMessage); err != nil {
					return err
				}
				if value.Status == "applied" && value.Revision > appliedRevision {
					appliedRevision = value.Revision
				}
			default:
				return errors.New("Agent sent unsupported stream message")
			}
		case <-poll.C:
			if err := s.sendDesired(ctx, connection, nodeID, appliedRevision, &lastSent); err != nil {
				return err
			}
		case <-recheck.C:
			authCtx, authCancel := context.WithTimeout(ctx, 3*time.Second)
			currentID, err := s.auth.AuthenticateCertificate(authCtx, certificate)
			authCancel()
			if err != nil || currentID != nodeID {
				return errors.New("Agent certificate revoked during stream")
			}
		}
	}
}

func (s *AgentStreamHandler) sendDesired(ctx context.Context, connection *websocket.Conn, nodeID string, applied int64, lastSent *int64) error {
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	desired, err := s.revisions.Desired(queryCtx, nodeID)
	cancel()
	if errors.Is(err, orchestration.ErrRevisionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if (desired.Revision <= applied && desired.Status == "applied") || desired.Revision <= *lastSent {
		return nil
	}
	payload, err := json.Marshal(agentproto.ConfigSnapshot{Revision: desired.Revision, SHA256: desired.Digest,
		ValidUntil: time.Now().Add(5 * time.Minute), ForwardConfig: desired.Snapshot.Rules})
	if err != nil {
		return err
	}
	messageID, err := id.NewV7()
	if err != nil {
		return err
	}
	frame, err := agentproto.Encode(agentproto.Envelope{ProtocolVersion: agentproto.ProtocolVersion,
		MessageID: messageID, NodeID: nodeID, Type: agentproto.TypeConfigSnapshot,
		SentAt: time.Now().UTC(), Payload: payload})
	if err != nil {
		return err
	}
	writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
	err = connection.Write(writeCtx, websocket.MessageText, frame)
	writeCancel()
	if err != nil {
		return err
	}
	*lastSent = desired.Revision
	return nil
}

func freshAgentMessage(sentAt time.Time) bool {
	age := time.Since(sentAt)
	return age >= -2*time.Minute && age <= 2*time.Minute
}
