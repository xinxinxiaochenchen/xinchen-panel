package agentclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"controlplane/internal/agentmetrics"
	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
	"controlplane/internal/platform/id"
	"github.com/coder/websocket"
)

type Runtime interface {
	Apply(context.Context, agentruntime.Snapshot) error
	Stats() agentruntime.Stats
	Close() error
}

type StateStore interface {
	Save(agentproto.ConfigSnapshot) error
}

type Config struct {
	URL               string
	NodeID            string
	Version           string
	RootCAs           *x509.CertPool
	Certificate       tls.Certificate
	HeartbeatInterval time.Duration
	ReconnectMin      time.Duration
	ReconnectMax      time.Duration
	ProxyReady        bool
}

type Client struct {
	config     Config
	newRuntime func() Runtime
	state      StateStore
	httpClient *http.Client
	metrics    *agentmetrics.Collector
}

func New(config Config, newRuntime func() Runtime, state StateStore) (*Client, error) {
	endpoint, err := url.Parse(config.URL)
	if err != nil || endpoint.Scheme != "wss" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" ||
		endpoint.Path != "/api/v1/agent/stream" || endpoint.RawQuery != "" || config.RootCAs == nil ||
		len(config.Certificate.Certificate) == 0 || config.NodeID == "" || config.Version == "" || newRuntime == nil || state == nil {
		return nil, errors.New("invalid Agent client configuration")
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 15 * time.Second
	}
	if config.ReconnectMin <= 0 {
		config.ReconnectMin = time.Second
	}
	if config.ReconnectMax <= 0 {
		config.ReconnectMax = 30 * time.Second
	}
	if config.ReconnectMax < config.ReconnectMin {
		return nil, errors.New("invalid Agent reconnect interval")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13,
		RootCAs: config.RootCAs, Certificates: []tls.Certificate{config.Certificate}}}
	return &Client{config: config, newRuntime: newRuntime, state: state,
		httpClient: &http.Client{Transport: transport}, metrics: agentmetrics.NewCollector(nil)}, nil
}

func (c *Client) Run(ctx context.Context) error {
	backoff := c.config.ReconnectMin
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		started := time.Now()
		_ = c.RunOnce(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Since(started) > time.Minute {
			backoff = c.config.ReconnectMin
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < c.config.ReconnectMax/2 {
			backoff *= 2
		} else {
			backoff = c.config.ReconnectMax
		}
	}
}

// RunOnce owns a fresh runtime for one authenticated connection. Closing the
// runtime on disconnect fails closed; reconnects request the full snapshot.
func (c *Client) RunOnce(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, c.config.URL, &websocket.DialOptions{HTTPClient: c.httpClient,
		CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return fmt.Errorf("dial Agent stream: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(agentproto.MaxFrameBytes)
	runtime := c.newRuntime()
	defer runtime.Close()
	var writeMu sync.Mutex
	send := func(kind agentproto.MessageType, payload any) error {
		messageID, err := id.NewV7()
		if err != nil {
			return err
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		frame, err := agentproto.Encode(agentproto.Envelope{ProtocolVersion: agentproto.ProtocolVersion,
			MessageID: messageID, NodeID: c.config.NodeID, Type: kind, SentAt: time.Now().UTC(), Payload: body})
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return conn.Write(writeCtx, websocket.MessageText, frame)
	}
	capabilities := []string{"forward"}
	if c.config.ProxyReady {
		capabilities = append(capabilities, "proxy")
	}
	if err := send(agentproto.TypeHello, agentproto.Hello{AgentVersion: c.config.Version,
		AppliedRevision: 0, Capabilities: capabilities}); err != nil {
		return err
	}
	started := time.Now()
	stopHeartbeats := make(chan struct{})
	defer close(stopHeartbeats)
	go func() {
		ticker := time.NewTicker(c.config.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopHeartbeats:
				return
			case <-ticker.C:
				stats := runtime.Stats()
				heartbeat := agentproto.Heartbeat{UptimeSeconds: int64(time.Since(started).Seconds()),
					Connections: stats.Connections, EngineStatus: "running"}
				if metrics, err := c.metrics.Sample(); err == nil {
					heartbeat.CPUPct = metrics.CPUPct
					heartbeat.MemoryUsedBytes = metrics.MemoryUsedBytes
					heartbeat.RXBytes = metrics.RXBytes
					heartbeat.TXBytes = metrics.TXBytes
				} else {
					heartbeat.EngineStatus = "degraded"
				}
				_ = send(agentproto.TypeHeartbeat, heartbeat)
			}
		}
	}()
	applied := int64(0)
	appliedDigest := ""
	var leaseUntil time.Time
	for {
		readCtx := ctx
		var cancelRead context.CancelFunc
		if !leaseUntil.IsZero() {
			readCtx, cancelRead = context.WithDeadline(ctx, leaseUntil)
		}
		kind, frame, err := conn.Read(readCtx)
		if cancelRead != nil {
			cancelRead()
		}
		if err != nil {
			return err
		}
		if kind != websocket.MessageText {
			return errors.New("control plane sent non-text frame")
		}
		message, err := agentproto.Decode(frame)
		if err != nil || message.NodeID != c.config.NodeID || message.Type != agentproto.TypeConfigSnapshot ||
			!freshMessage(message.SentAt) {
			return errors.New("invalid control plane message")
		}
		snapshot, err := agentproto.DecodeConfigSnapshot(message.Payload, time.Now())
		if err != nil {
			return fmt.Errorf("invalid configuration snapshot: %w", err)
		}
		if err := validateReceivedRevision(applied, appliedDigest, snapshot.Revision, snapshot.SHA256); err != nil {
			return err
		}
		if snapshot.Revision > applied {
			applyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err = runtime.Apply(applyCtx, agentruntime.Snapshot{Revision: uint64(snapshot.Revision), Rules: snapshot.ForwardConfig, ProxyConfig: snapshot.ProxyConfig})
			cancel()
			if err != nil {
				if sendErr := send(agentproto.TypeConfigResult, agentproto.ConfigResult{Revision: snapshot.Revision,
					SHA256: snapshot.SHA256, Status: "rejected", ErrorCode: "APPLY_FAILED", ErrorMessage: "configuration could not be applied"}); sendErr != nil {
					return sendErr
				}
				continue
			}
			applied = snapshot.Revision
			appliedDigest = snapshot.SHA256
		}
		if err := c.state.Save(snapshot); err != nil {
			return fmt.Errorf("persist applied Agent snapshot: %w", err)
		}
		leaseUntil = snapshot.ValidUntil
		if err := send(agentproto.TypeConfigResult, agentproto.ConfigResult{Revision: snapshot.Revision,
			SHA256: snapshot.SHA256, Status: "applied"}); err != nil {
			return err
		}
	}
}

func validateReceivedRevision(applied int64, digest string, received int64, receivedDigest string) error {
	if received < applied {
		return errors.New("stale control plane snapshot")
	}
	if received == applied && receivedDigest != digest {
		return errors.New("control plane changed configuration without a new revision")
	}
	return nil
}

func freshMessage(sentAt time.Time) bool {
	age := time.Since(sentAt)
	return age >= -2*time.Minute && age <= 2*time.Minute
}
