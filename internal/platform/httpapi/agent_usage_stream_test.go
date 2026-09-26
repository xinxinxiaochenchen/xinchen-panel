package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
	"controlplane/internal/orchestration"
	"github.com/coder/websocket"
)

type usageRecorderStub struct {
	mu      sync.Mutex
	nodeID  string
	batches []agentproto.UsageBatch
	fail    bool
}

func (s *usageRecorderStub) RecordBatch(_ context.Context, nodeID string, batch agentproto.UsageBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeID = nodeID
	s.batches = append(s.batches, batch)
	if s.fail {
		return errors.New("database unavailable")
	}
	return nil
}

func TestAgentStreamAcknowledgesOnlyPersistedUsageFromAuthenticatedNode(t *testing.T) {
	_, digest, _ := agentproto.CanonicalForwardConfig(nil)
	store := &streamStore{desired: orchestration.DesiredRevision{NodeID: certificateTestNodeID, Revision: 1, Digest: digest, Snapshot: agentruntime.Snapshot{Revision: 1}}, results: make(chan agentproto.ConfigResult, 1), heartbeats: make(chan agentproto.Heartbeat, 1)}
	usage := &usageRecorderStub{}
	stream := NewAgentStreamHandler(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), &streamAuthenticator{}, store, store, usage)
	server := httptest.NewUnstartedServer(NewAgentHandlerWithStream(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, stream))
	pair, roots := testStreamCertificate(t)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots}
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{pair}
	client.Transport = transport
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	connect := func() *websocket.Conn {
		conn, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(server.URL, "https")+"/api/v1/agent/stream", &websocket.DialOptions{HTTPClient: client})
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	send := func(conn *websocket.Conn, kind agentproto.MessageType, payload any, nodeID string) {
		body, _ := json.Marshal(payload)
		frame, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", NodeID: nodeID, Type: kind, SentAt: time.Now(), Payload: body})
		if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
	}
	batch := agentproto.UsageBatch{BatchID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", Reports: []agentproto.UsageReport{{ConnectionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", LeaseID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Sequence: 1, UploadedBytes: 4, DownloadedBytes: 8, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}}}
	for _, fail := range []bool{false, true} {
		usage.mu.Lock()
		usage.fail = fail
		usage.mu.Unlock()
		conn := connect()
		send(conn, agentproto.TypeHello, agentproto.Hello{AgentVersion: "1.0", AppliedRevision: 0, Capabilities: []string{"forward"}}, certificateTestNodeID)
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, _ := agentproto.Decode(raw)
		if snapshot.Type != agentproto.TypeConfigSnapshot {
			t.Fatalf("first message = %s", snapshot.Type)
		}
		send(conn, agentproto.TypeUsageBatch, batch, certificateTestNodeID)
		_, raw, err = conn.Read(ctx)
		if fail {
			if err == nil {
				t.Fatal("ACKed unpersisted batch")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			ackEnvelope, err := agentproto.Decode(raw)
			if err != nil || ackEnvelope.Type != agentproto.TypeUsageAck {
				t.Fatalf("ack envelope = %+v %v", ackEnvelope, err)
			}
			ack, err := agentproto.DecodeUsageAck(ackEnvelope.Payload)
			want, _ := agentproto.UsageBatchDigest(batch)
			if err != nil || ack.BatchID != batch.BatchID || ack.SHA256 != want {
				t.Fatalf("ack = %+v %v", ack, err)
			}
		}
		conn.CloseNow()
	}
	usage.mu.Lock()
	defer usage.mu.Unlock()
	if usage.nodeID != certificateTestNodeID || len(usage.batches) != 2 {
		t.Fatalf("recorder = %+v", usage)
	}
}
