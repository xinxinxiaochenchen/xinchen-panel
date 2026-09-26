package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"controlplane/internal/billing"
)

type usageStoreStub struct {
	node         string
	agent        string
	lookupErr    error
	calls        []billing.UsageReport
	ids          []string
	failSequence int64
}

func (s *usageStoreStub) AgentIDForNode(_ context.Context, node string) (string, error) {
	s.node = node
	return s.agent, s.lookupErr
}
func (s *usageStoreStub) RecordUsage(_ context.Context, agent string, report billing.UsageReport) (billing.UsageEvent, error) {
	s.ids = append(s.ids, agent)
	s.calls = append(s.calls, report)
	if report.Sequence == s.failSequence {
		return billing.UsageEvent{}, billing.ErrConflict
	}
	return billing.UsageEvent{}, nil
}

func TestUsageRecorderMapsAuthenticatedNodeAndDoesNotAcknowledgePartialFailure(t *testing.T) {
	const node = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const agent = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const connection = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	const lease = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	batch := agentproto.UsageBatch{BatchID: node, Reports: []agentproto.UsageReport{{ConnectionID: connection, LeaseID: lease, Sequence: 1, UploadedBytes: 10, DownloadedBytes: 20, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}, {ConnectionID: connection, LeaseID: lease, Sequence: 2, UploadedBytes: 20, DownloadedBytes: 30, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}}}
	store := &usageStoreStub{agent: agent, failSequence: 2}
	recorder := NewUsageRecorder(store)
	if err := recorder.RecordBatch(context.Background(), node, batch); !errors.Is(err, billing.ErrConflict) {
		t.Fatalf("partial failure = %v", err)
	}
	if store.node != node || len(store.calls) != 2 || store.ids[0] != agent || store.ids[1] != agent {
		t.Fatalf("identity mapping = %+v", store)
	}
	if store.calls[0].LeaseID != lease || store.calls[0].Counters.UploadedBytes != 10 {
		t.Fatalf("mapped report = %+v", store.calls[0])
	}
	store.failSequence = 0
	if err := recorder.RecordBatch(context.Background(), node, batch); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 4 {
		t.Fatal("retry did not replay the complete batch")
	}
	store.lookupErr = billing.ErrNotFound
	if err := recorder.RecordBatch(context.Background(), node, batch); !errors.Is(err, billing.ErrNotFound) {
		t.Fatalf("unknown node = %v", err)
	}
	if len(store.calls) != 4 {
		t.Fatal("reported usage without authenticated Agent mapping")
	}
}
