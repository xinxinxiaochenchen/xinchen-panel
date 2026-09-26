package agentclient

import (
	"context"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
)

func TestQuotaAdapterMapsRuntimeOpenToProtocolGrant(t *testing.T) {
	request := agentruntime.QuotaOpenRequest{
		RequestID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ConnectionID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		ResourceKind: "forward", ResourceID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", Revision: 3, RequestedBytes: 4096,
	}
	issued := time.Now().UTC().Truncate(time.Microsecond)
	sent := make(chan agentproto.ConnectionOpen, 1)
	exchange := NewQuotaExchange(func(kind agentproto.MessageType, payload any) error {
		if kind != agentproto.TypeConnectionOpen {
			t.Errorf("kind=%s", kind)
		}
		sent <- payload.(agentproto.ConnectionOpen)
		return nil
	})
	adapter := NewRuntimeQuotaService(exchange)
	results := make(chan agentruntime.QuotaLease, 1)
	errors := make(chan error, 1)
	go func() {
		lease, err := adapter.Open(context.Background(), request)
		results <- lease
		errors <- err
	}()
	wire := <-sent
	if wire.RequestID != request.RequestID || wire.ConnectionID != request.ConnectionID || wire.ResourceKind != request.ResourceKind || wire.ResourceID != request.ResourceID || wire.Revision != request.Revision || wire.RequestedBytes != request.RequestedBytes {
		t.Fatalf("wire open=%+v", wire)
	}
	grant := agentproto.QuotaGrant{RequestID: request.RequestID, ConnectionID: request.ConnectionID,
		PeriodID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", MultiplierMilli: 1500,
		LeaseID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", GrantedBytes: 2048, LeaseState: "active",
		IssuedAt: issued, ExpiresAt: issued.Add(30 * time.Second), PeriodEndsAt: issued.Add(time.Hour)}
	if err := exchange.DeliverGrant(grant); err != nil {
		t.Fatal(err)
	}
	lease := <-results
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if lease.ID != grant.LeaseID || lease.ConnectionID != request.ConnectionID || lease.PeriodID != grant.PeriodID || lease.MultiplierMilli != 1500 || lease.GrantedBytes != 2048 || !lease.ExpiresAt.Equal(grant.ExpiresAt) {
		t.Fatalf("runtime lease=%+v", lease)
	}
}
