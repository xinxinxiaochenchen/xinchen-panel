package agentclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/agentproto"
)

func TestQuotaExchangeMatchesRepliesAndRejectsDuplicates(t *testing.T) {
	const connectionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const requestID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const leaseID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	issued := time.Now().UTC().Truncate(time.Microsecond)
	sent := make(chan agentproto.ConnectionOpen, 1)
	exchange := NewQuotaExchange(func(kind agentproto.MessageType, payload any) error {
		if kind != agentproto.TypeConnectionOpen {
			t.Errorf("sent %s", kind)
		}
		sent <- payload.(agentproto.ConnectionOpen)
		return nil
	})
	request := agentproto.ConnectionOpen{RequestID: requestID, ConnectionID: connectionID,
		ResourceKind: "forward", ResourceID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", Revision: 1, RequestedBytes: 1024}
	result := make(chan agentproto.QuotaGrant, 1)
	failures := make(chan error, 1)
	go func() {
		grant, err := exchange.Open(context.Background(), request)
		result <- grant
		failures <- err
	}()
	<-sent
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := exchange.Open(ctx, request); !errors.Is(err, ErrQuotaRequestInFlight) {
		t.Fatalf("duplicate pending request=%v", err)
	}
	grant := agentproto.QuotaGrant{RequestID: requestID, ConnectionID: connectionID,
		PeriodID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", MultiplierMilli: 500,
		LeaseID: leaseID, GrantedBytes: 1024, LeaseState: "active", IssuedAt: issued,
		ExpiresAt: issued.Add(30 * time.Second), PeriodEndsAt: issued.Add(time.Hour)}
	if err := exchange.DeliverGrant(grant); err != nil {
		t.Fatal(err)
	}
	if got := <-result; got.LeaseID != leaseID {
		t.Fatalf("grant=%+v", got)
	}
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	if err := exchange.DeliverGrant(grant); !errors.Is(err, ErrUnexpectedQuotaReply) {
		t.Fatalf("duplicate reply=%v", err)
	}
}

func TestQuotaExchangeDenialCancellationAndClose(t *testing.T) {
	const connectionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const firstID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const secondID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	sent := make(chan agentproto.MessageType, 2)
	exchange := NewQuotaExchange(func(kind agentproto.MessageType, _ any) error { sent <- kind; return nil })
	request := agentproto.QuotaRequest{RequestID: firstID, ConnectionID: connectionID, Revision: 1, RequestedBytes: 128}
	result := make(chan error, 1)
	go func() { _, err := exchange.Renew(context.Background(), request); result <- err }()
	<-sent
	if err := exchange.DeliverDenied(agentproto.QuotaDenied{RequestID: firstID, ConnectionID: connectionID, ErrorCode: "QUOTA_EXHAUSTED"}); err != nil {
		t.Fatal(err)
	}
	var denied *QuotaDeniedError
	if err := <-result; !errors.As(err, &denied) || denied.Code != "QUOTA_EXHAUSTED" {
		t.Fatalf("denial=%v", err)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	request.RequestID = secondID
	go func() { _, err := exchange.Renew(cancelCtx, request); result <- err }()
	<-sent
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	if err := exchange.DeliverDenied(agentproto.QuotaDenied{RequestID: secondID, ConnectionID: connectionID, ErrorCode: "RETRY_LATER"}); !errors.Is(err, ErrUnexpectedQuotaReply) {
		t.Fatalf("late reply=%v", err)
	}
	request.RequestID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	go func() { _, err := exchange.Renew(context.Background(), request); result <- err }()
	<-sent
	exchange.Close()
	if err := <-result; !errors.Is(err, ErrQuotaExchangeClosed) {
		t.Fatalf("pending request after stream closed=%v", err)
	}
	if _, err := exchange.Renew(context.Background(), request); !errors.Is(err, ErrQuotaExchangeClosed) {
		t.Fatalf("closed exchange=%v", err)
	}
}

func TestQuotaExchangeSettlementRequiresMatchingLeaseAndConsumption(t *testing.T) {
	request := agentproto.QuotaSettle{RequestID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		ConnectionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", LeaseID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", ConsumedBytes: 128}
	sent := make(chan agentproto.MessageType, 1)
	exchange := NewQuotaExchange(func(kind agentproto.MessageType, _ any) error { sent <- kind; return nil })
	result := make(chan error, 1)
	go func() { _, err := exchange.Settle(context.Background(), request); result <- err }()
	if kind := <-sent; kind != agentproto.TypeQuotaSettle {
		t.Fatalf("sent %s", kind)
	}
	if err := exchange.DeliverSettled(agentproto.QuotaSettled{RequestID: request.RequestID, ConnectionID: request.ConnectionID,
		LeaseID: request.LeaseID, ConsumedBytes: 127}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrUnexpectedQuotaReply) {
		t.Fatalf("mismatched settlement=%v", err)
	}
}
