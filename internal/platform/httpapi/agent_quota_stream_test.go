package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
	"controlplane/internal/billing"
	"controlplane/internal/orchestration"
	"github.com/coder/websocket"
)

type quotaStreamStub struct {
	nodeID    string
	open      billing.OpenRequest
	renew     billing.RenewRequest
	settleID  string
	settleSum int64
	openErr   error
	grant     billing.AdmissionGrant
}

func (s *quotaStreamStub) OpenConnection(_ context.Context, nodeID string, req billing.OpenRequest) (billing.AdmissionGrant, error) {
	s.nodeID, s.open = nodeID, req
	return s.grant, s.openErr
}
func (s *quotaStreamStub) RenewConnectionLease(_ context.Context, nodeID string, req billing.RenewRequest) (billing.AdmissionGrant, error) {
	s.nodeID, s.renew = nodeID, req
	return s.grant, nil
}
func (s *quotaStreamStub) SettleConnectionLease(_ context.Context, nodeID, connectionID, leaseID string, consumed int64) (billing.Lease, error) {
	s.nodeID, s.settleID, s.settleSum = nodeID, connectionID, consumed
	if leaseID != s.grant.Lease.ID {
		return billing.Lease{}, billing.ErrNotFound
	}
	lease := s.grant.Lease
	lease.State = "settled"
	lease.ConsumedBytes = consumed
	return lease, nil
}

func TestAgentQuotaStreamUsesAuthenticatedNodeAndReturnsGrants(t *testing.T) {
	_, digest, _ := agentproto.CanonicalForwardConfig(nil)
	store := &streamStore{desired: orchestration.DesiredRevision{NodeID: certificateTestNodeID, Revision: 1, Digest: digest, Snapshot: agentruntime.Snapshot{Revision: 1}}, results: make(chan agentproto.ConfigResult, 1), heartbeats: make(chan agentproto.Heartbeat, 1)}
	issued := time.Now().UTC().Truncate(time.Microsecond)
	quota := &quotaStreamStub{grant: billing.AdmissionGrant{
		ConnectionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", MultiplierMilli: 500, PeriodEndsAt: issued.Add(time.Hour),
		Lease: billing.Lease{ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", PeriodID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
			RequestedBytes: 1024, GrantedBytes: 1024, IssuedAt: issued, ExpiresAt: issued.Add(30 * time.Second), State: "active"},
	}}
	stream := NewAgentStreamHandler(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), &streamAuthenticator{}, store, store)
	stream.SetQuotaService(quota)
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
	conn, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(server.URL, "https")+"/api/v1/agent/stream", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	send := func(kind agentproto.MessageType, payload any) {
		body, _ := json.Marshal(payload)
		frame, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", NodeID: certificateTestNodeID, Type: kind, SentAt: time.Now(), Payload: body})
		if err := conn.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
	}
	read := func() agentproto.Envelope {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		message, err := agentproto.Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		return message
	}
	send(agentproto.TypeHello, agentproto.Hello{AgentVersion: "1.0", AppliedRevision: 0, Capabilities: []string{"forward"}})
	if first := read(); first.Type != agentproto.TypeConfigSnapshot {
		t.Fatalf("first message=%s", first.Type)
	}
	send(agentproto.TypeConfigResult, agentproto.ConfigResult{Revision: 1, SHA256: digest, Status: "applied"})
	open := agentproto.ConnectionOpen{RequestID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", ConnectionID: quota.grant.ConnectionID,
		ResourceKind: "forward", ResourceID: "ffffffff-ffff-4fff-8fff-ffffffffffff", Revision: 1, RequestedBytes: 1024}
	send(agentproto.TypeConnectionOpen, open)
	message := read()
	grant, err := agentproto.DecodeQuotaGrant(message.Payload)
	if err != nil || message.Type != agentproto.TypeQuotaGrant || grant.RequestID != open.RequestID || grant.LeaseID != quota.grant.Lease.ID || quota.nodeID != certificateTestNodeID || quota.open.ResourceID != open.ResourceID {
		t.Fatalf("open response=%+v grant=%+v err=%v store=%+v", message, grant, err, quota)
	}
	renew := agentproto.QuotaRequest{RequestID: "11111111-1111-4111-8111-111111111111", ConnectionID: open.ConnectionID, Revision: 1, RequestedBytes: 1024}
	send(agentproto.TypeQuotaRequest, renew)
	message = read()
	grant, err = agentproto.DecodeQuotaGrant(message.Payload)
	if err != nil || message.Type != agentproto.TypeQuotaGrant || grant.RequestID != renew.RequestID || quota.renew.ConnectionID != open.ConnectionID {
		t.Fatalf("renew response=%+v grant=%+v err=%v", message, grant, err)
	}
	settle := agentproto.QuotaSettle{RequestID: "22222222-2222-4222-8222-222222222222", ConnectionID: open.ConnectionID, LeaseID: quota.grant.Lease.ID, ConsumedBytes: 128}
	send(agentproto.TypeQuotaSettle, settle)
	message = read()
	settled, err := agentproto.DecodeQuotaSettled(message.Payload)
	if err != nil || message.Type != agentproto.TypeQuotaSettled || settled.RequestID != settle.RequestID || settled.ConsumedBytes != 128 || quota.settleID != open.ConnectionID || quota.settleSum != 128 {
		t.Fatalf("settle response=%+v settled=%+v err=%v", message, settled, err)
	}
	quota.openErr = billing.ErrQuotaExhausted
	open.RequestID = "33333333-3333-4333-8333-333333333333"
	send(agentproto.TypeConnectionOpen, open)
	message = read()
	denied, err := agentproto.DecodeQuotaDenied(message.Payload)
	if err != nil || message.Type != agentproto.TypeQuotaDenied || denied.ErrorCode != "QUOTA_EXHAUSTED" || denied.RequestID != open.RequestID {
		t.Fatalf("denied response=%+v denied=%+v err=%v", message, denied, err)
	}
}
