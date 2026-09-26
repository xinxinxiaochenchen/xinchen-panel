package httpapi

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"controlplane/internal/agentidentity"
	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
	"controlplane/internal/billing"
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
	MarkOnline(context.Context, string, string, []string) error
	RecordHeartbeat(context.Context, string, agentproto.Heartbeat) error
	MarkOffline(context.Context, string) error
}

// AgentStreamUsage resolves the authenticated node to its Agent identity and
// persists each report before returning. Partial persistence is safe: a
// replay retries the whole batch against the idempotent usage ledger.
type AgentStreamUsage interface {
	RecordBatch(context.Context, string, agentproto.UsageBatch) error
}

// AgentStreamQuota uses the authenticated node identity from the mTLS stream.
// The Agent cannot supply user, period or multiplier values.
type AgentStreamQuota interface {
	OpenConnection(context.Context, string, billing.OpenRequest) (billing.AdmissionGrant, error)
	RenewConnectionLease(context.Context, string, billing.RenewRequest) (billing.AdmissionGrant, error)
	SettleConnectionLease(context.Context, string, string, string, int64) (billing.Lease, error)
}

type AgentStreamHandler struct {
	parent       context.Context
	logger       *slog.Logger
	auth         AgentCertificateAuthenticator
	revisions    AgentStreamRevisions
	presence     AgentStreamPresence
	usage        AgentStreamUsage
	quota        AgentStreamQuota
	mu           sync.Mutex
	active       map[string]*websocket.Conn
	pollEvery    time.Duration
	renewEvery   time.Duration
	recheckEvery time.Duration
}

// SetQuotaService must be called before the handler starts serving requests.
func (s *AgentStreamHandler) SetQuotaService(quota AgentStreamQuota) { s.quota = quota }

func NewAgentStreamHandler(parent context.Context, logger *slog.Logger, auth AgentCertificateAuthenticator,
	revisions AgentStreamRevisions, presence AgentStreamPresence, usage ...AgentStreamUsage) *AgentStreamHandler {
	var recorder AgentStreamUsage
	if len(usage) > 0 {
		recorder = usage[0]
	}
	return &AgentStreamHandler{parent: parent, logger: logger, auth: auth, revisions: revisions, usage: recorder,
		presence: presence, active: make(map[string]*websocket.Conn), pollEvery: 3 * time.Second,
		renewEvery:   time.Minute,
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
	if err := s.presence.MarkOnline(ctx, nodeID, hello.AgentVersion, hello.Capabilities); err != nil {
		return err
	}
	if reconciler, ok := s.revisions.(interface {
		Reconcile(context.Context, string) (orchestration.DesiredRevision, bool, error)
	}); ok {
		if _, _, err := reconciler.Reconcile(ctx, nodeID); err != nil {
			return err
		}
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
	activeCertificate := certificate
	lastSent := int64(0)
	var lastRenewed time.Time
	if err := s.sendDesired(ctx, connection, nodeID, hello.Capabilities, appliedRevision, &lastSent, &lastRenewed); err != nil {
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
			case agentproto.TypeCertificateUpdate:
				updated, fingerprint, err := s.acceptCertificateUpdate(ctx, nodeID, certificate, activeCertificate, envelope.Payload)
				if err != nil {
					return err
				}
				activeCertificate = updated
				if err := s.sendCertificateUpdateAck(ctx, connection, nodeID, fingerprint); err != nil {
					return err
				}
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
			case agentproto.TypeUsageBatch:
				if s.usage == nil {
					return errors.New("Agent usage recording is unavailable")
				}
				batch, err := agentproto.DecodeUsageBatch(envelope.Payload)
				if err != nil {
					return err
				}
				recordCtx, recordCancel := context.WithTimeout(ctx, 10*time.Second)
				err = s.usage.RecordBatch(recordCtx, nodeID, batch)
				recordCancel()
				if err != nil {
					return err
				}
				if err := s.sendUsageAck(ctx, connection, nodeID, batch); err != nil {
					return err
				}
			case agentproto.TypeConnectionOpen, agentproto.TypeQuotaRequest, agentproto.TypeQuotaSettle:
				if err := s.handleQuotaMessage(ctx, connection, nodeID, envelope); err != nil {
					return err
				}
			default:
				return errors.New("Agent sent unsupported stream message")
			}
		case <-poll.C:
			if err := s.sendDesired(ctx, connection, nodeID, hello.Capabilities, appliedRevision, &lastSent, &lastRenewed); err != nil {
				return err
			}
		case <-recheck.C:
			authCtx, authCancel := context.WithTimeout(ctx, 3*time.Second)
			currentID, err := s.auth.AuthenticateCertificate(authCtx, activeCertificate)
			authCancel()
			if err != nil || currentID != nodeID {
				return errors.New("Agent certificate revoked during stream")
			}
		}
	}
}

func (s *AgentStreamHandler) sendUsageAck(ctx context.Context, connection *websocket.Conn, nodeID string, batch agentproto.UsageBatch) error {
	digest, err := agentproto.UsageBatchDigest(batch)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(agentproto.UsageAck{BatchID: batch.BatchID, SHA256: digest})
	if err != nil {
		return err
	}
	messageID, err := id.NewV7()
	if err != nil {
		return err
	}
	frame, err := agentproto.Encode(agentproto.Envelope{ProtocolVersion: agentproto.ProtocolVersion,
		MessageID: messageID, NodeID: nodeID, Type: agentproto.TypeUsageAck,
		SentAt: time.Now().UTC(), Payload: payload})
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return connection.Write(writeCtx, websocket.MessageText, frame)
}

func (s *AgentStreamHandler) sendDesired(ctx context.Context, connection *websocket.Conn, nodeID string, capabilities []string, applied int64, lastSent *int64, lastRenewed *time.Time) error {
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	desired, err := s.revisions.Desired(queryCtx, nodeID)
	cancel()
	if errors.Is(err, orchestration.ErrRevisionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// Re-send the current revision periodically to renew the Agent's short
	// execution lease. The Agent closes listeners if renewal stops.
	if desired.Revision < 1 || desired.Revision < applied || (desired.Revision == *lastSent && time.Since(*lastRenewed) < s.renewEvery) {
		return nil
	}
	if err := requireAgentSnapshotCapability(capabilities, desired.Snapshot); err != nil {
		if desired.Revision != *lastSent {
			if err := s.revisions.RecordResult(ctx, nodeID, desired.Revision, desired.Digest, "rejected", "CAPABILITY_UNAVAILABLE", "Agent proxy TLS capability is not ready"); err != nil {
				return err
			}
			*lastSent = desired.Revision
		}
		return nil
	}
	payload, err := json.Marshal(agentproto.ConfigSnapshot{Revision: desired.Revision, SHA256: desired.Digest,
		ValidUntil: time.Now().Add(5 * time.Minute), ForwardConfig: desired.Snapshot.Rules, ProxyConfig: desired.Snapshot.ProxyConfig})
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
	*lastRenewed = time.Now()
	return nil
}

func requireAgentSnapshotCapability(capabilities []string, snapshot agentruntime.Snapshot) error {
	if len(snapshot.ProxyConfig) > 0 && !slices.Contains(capabilities, "proxy") {
		return errors.New("Agent proxy TLS capability is not ready")
	}
	return nil
}

func freshAgentMessage(sentAt time.Time) bool {
	age := time.Since(sentAt)
	return age >= -2*time.Minute && age <= 2*time.Minute
}
