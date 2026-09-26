package agentclient

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"controlplane/internal/agentproto"
)

func TestRuntimeQuotaMeterRecoversInterruptedWriteThenSettlesAfterAck(t *testing.T) {
	dir := t.TempDir()
	outbox, err := NewFileUsageOutbox(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	leases, err := NewFileLeaseStore(filepath.Join(dir, "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	state := testLeaseState()
	state.InFlightUploadBytes = 7
	if err := leases.Put(state); err != nil {
		t.Fatal(err)
	}
	if err := leases.Close(); err != nil {
		t.Fatal(err)
	}
	if err := outbox.Close(); err != nil {
		t.Fatal(err)
	}
	outbox, err = NewFileUsageOutbox(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer outbox.Close()
	leases, err = NewFileLeaseStore(filepath.Join(dir, "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer leases.Close()
	quota := &meterQuotaStub{}
	meter := NewRuntimeQuotaMeter(quota, outbox, leases).(*RuntimeQuotaMeter)
	if err := meter.PrepareRecovery(); err != nil {
		t.Fatal(err)
	}
	pending := outbox.Pending()
	if len(pending) != 1 || pending[0].Reports[0].UploadedBytes != 7 || pending[0].Reports[0].Sequence != 1 {
		t.Fatalf("recovered usage = %+v", pending)
	}
	if err := meter.SettleRecovered(context.Background()); err == nil {
		t.Fatal("settled before ACK")
	}
	ack := agentproto.UsageAck{BatchID: pending[0].BatchID}
	ack.SHA256, err = agentproto.UsageBatchDigest(pending[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Acknowledge(ack); err != nil {
		t.Fatal(err)
	}
	if err := meter.SettleRecovered(context.Background()); err != nil {
		t.Fatal(err)
	}
	if quota.settle.ConsumedBytes != 10 || len(leases.Pending()) != 0 {
		t.Fatalf("settlement = %+v leases=%+v", quota.settle, leases.Pending())
	}
}

func TestRuntimeQuotaMeterRecoveryChargesOnlyCurrentLeaseDelta(t *testing.T) {
	dir := t.TempDir()
	outbox, err := NewFileUsageOutbox(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer outbox.Close()
	leases, err := NewFileLeaseStore(filepath.Join(dir, "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer leases.Close()
	state := testLeaseState()
	state.MultiplierMilli = 1000
	state.UploadedBytes = 5
	state.ReportedUploadedBytes = 5
	state.Sequence = 1
	state.InFlightUploadBytes = 3
	state.InFlightUploadBase = 5
	if err := leases.Put(state); err != nil {
		t.Fatal(err)
	}
	meter := NewRuntimeQuotaMeter(&meterQuotaStub{}, outbox, leases).(*RuntimeQuotaMeter)
	if err := meter.PrepareRecovery(); err != nil {
		t.Fatal(err)
	}
	got := leases.Pending()[0]
	if got.UploadedBytes != 8 || got.ConsumedBytes != 3 || got.Sequence != 2 {
		t.Fatalf("renewed lease recovery = %+v", got)
	}
}

func TestRuntimeQuotaMeterReusesOutboxReportAfterInterruptedStateUpdate(t *testing.T) {
	dir := t.TempDir()
	outbox, err := NewFileUsageOutbox(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer outbox.Close()
	leases, err := NewFileLeaseStore(filepath.Join(dir, "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer leases.Close()
	state := testLeaseState()
	state.InFlightUploadBytes = 10
	if err := leases.Put(state); err != nil {
		t.Fatal(err)
	}
	batch := sampleUsageBatch()
	batch.Reports[0].ConnectionID = state.ConnectionID
	batch.Reports[0].LeaseID = state.LeaseID
	batch.Reports[0].UploadedBytes = 4
	batch.Reports[0].DownloadedBytes = 0
	batch.Reports[0].ObservedAt = state.IssuedAt.Add(time.Millisecond)
	if err := outbox.Enqueue(batch); err != nil {
		t.Fatal(err)
	}
	meter := NewRuntimeQuotaMeter(&meterQuotaStub{}, outbox, leases).(*RuntimeQuotaMeter)
	if err := meter.PrepareRecovery(); err != nil {
		t.Fatal(err)
	}
	if len(outbox.Pending()) != 2 || outbox.Pending()[1].Reports[0].UploadedBytes != 10 || outbox.Pending()[1].Reports[0].Sequence != 2 {
		t.Fatalf("missing conservative remainder: %+v", outbox.Pending())
	}
	got := leases.Pending()[0]
	if got.UploadedBytes != 10 || got.Sequence != 2 || got.InFlightUploadBytes != 0 {
		t.Fatalf("state = %+v", got)
	}
}

func TestRuntimeQuotaMeterRecoveryRetainsOppositeDirectionIntent(t *testing.T) {
	dir := t.TempDir()
	outbox, err := NewFileUsageOutbox(filepath.Join(dir, "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer outbox.Close()
	leases, err := NewFileLeaseStore(filepath.Join(dir, "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer leases.Close()
	state := testLeaseState()
	state.InFlightUploadBytes = 4
	state.InFlightDownloadBytes = 3
	if err := leases.Put(state); err != nil {
		t.Fatal(err)
	}
	batch := sampleUsageBatch()
	batch.Reports[0].ConnectionID = state.ConnectionID
	batch.Reports[0].LeaseID = state.LeaseID
	batch.Reports[0].UploadedBytes = 4
	batch.Reports[0].DownloadedBytes = 0
	batch.Reports[0].ObservedAt = state.IssuedAt.Add(time.Millisecond)
	if err := outbox.Enqueue(batch); err != nil {
		t.Fatal(err)
	}
	meter := NewRuntimeQuotaMeter(&meterQuotaStub{}, outbox, leases).(*RuntimeQuotaMeter)
	if err := meter.PrepareRecovery(); err != nil {
		t.Fatal(err)
	}
	pending := outbox.Pending()
	if len(pending) != 2 || pending[1].Reports[0].Sequence != 2 || pending[1].Reports[0].UploadedBytes != 4 || pending[1].Reports[0].DownloadedBytes != 3 {
		t.Fatalf("opposite direction write lost: %+v", pending)
	}
}
