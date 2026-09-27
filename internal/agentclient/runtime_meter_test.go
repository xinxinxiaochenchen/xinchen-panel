package agentclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"controlplane/internal/agentmeter"
	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
)

type meterQuotaStub struct {
	lease     agentruntime.QuotaLease
	open      agentruntime.QuotaOpenRequest
	settle    agentruntime.QuotaSettleRequest
	settleErr error
}

func (s *meterQuotaStub) Open(_ context.Context, req agentruntime.QuotaOpenRequest) (agentruntime.QuotaLease, error) {
	s.open = req
	s.lease.RequestID = req.RequestID
	s.lease.ConnectionID = req.ConnectionID
	return s.lease, nil
}
func (s *meterQuotaStub) Renew(context.Context, agentruntime.QuotaRenewRequest) (agentruntime.QuotaLease, error) {
	return agentruntime.QuotaLease{}, errors.New("renew not expected")
}
func (s *meterQuotaStub) Settle(_ context.Context, req agentruntime.QuotaSettleRequest) (agentruntime.QuotaSettlement, error) {
	s.settle = req
	if s.settleErr != nil {
		return agentruntime.QuotaSettlement{}, s.settleErr
	}
	return agentruntime.QuotaSettlement{RequestID: req.RequestID, ConnectionID: req.ConnectionID, LeaseID: req.LeaseID, ConsumedBytes: req.ConsumedBytes}, nil
}

func TestRuntimeQuotaMeterPropagatesFailedSettlementAfterAck(t *testing.T) {
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
	issued := time.Now().UTC().Truncate(time.Microsecond)
	failure := errors.New("control plane settlement failed")
	quota := &meterQuotaStub{settleErr: failure, lease: agentruntime.QuotaLease{PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MultiplierMilli: 1000, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", GrantedBytes: 8, IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), PeriodEndsAt: issued.Add(time.Hour)}}
	meter := NewRuntimeQuotaMeter(quota, outbox, leases).(*RuntimeQuotaMeter)
	connection, err := meter.Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Copy(io.Discard, bytes.NewBufferString("abc"), agentmeter.Upload); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); !errors.Is(err, ErrUsagePending) {
		t.Fatalf("close = %v", err)
	}
	batch := outbox.Pending()[0]
	digest, err := agentproto.UsageBatchDigest(batch)
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Acknowledge(agentproto.UsageAck{BatchID: batch.BatchID, SHA256: digest}); err != nil {
		t.Fatal(err)
	}
	if err := meter.HandleUsageAck(batch.BatchID); !errors.Is(err, failure) {
		t.Fatalf("settlement failure hidden: %v", err)
	}
	if len(leases.Pending()) != 1 {
		t.Fatal("failed settlement removed lease")
	}
}

func TestRuntimeQuotaMeterPersistsUsageBeforeCloseAndSettlesAfterAck(t *testing.T) {
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
	issued := time.Now().UTC().Truncate(time.Microsecond)
	quota := &meterQuotaStub{lease: agentruntime.QuotaLease{
		RequestID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", ConnectionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		MultiplierMilli: 1000, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", GrantedBytes: 8,
		IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), PeriodEndsAt: issued.Add(time.Hour),
	}}
	meter := NewRuntimeQuotaMeter(quota, outbox, leases)
	connection, err := meter.Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 7)
	if err != nil {
		t.Fatal(err)
	}
	if quota.open.ResourceKind != "forward" || quota.open.ResourceID != "dddddddd-dddd-4ddd-8ddd-dddddddddddd" || quota.open.Revision != 7 || quota.open.RequestedBytes != agentproto.MaxQuotaGrantBytes {
		t.Fatalf("open request = %+v", quota.open)
	}
	var received bytes.Buffer
	if n, err := connection.Copy(&received, bytes.NewBufferString("hello"), agentmeter.Upload); err != nil || n != 5 || received.String() != "hello" {
		t.Fatalf("copy = %d %v %q", n, err, received.String())
	}
	if len(outbox.Pending()) != 0 {
		t.Fatalf("per-write reports filled outbox: %+v", outbox.Pending())
	}
	stored := leases.Pending()
	if len(stored) != 1 || stored[0].UploadedBytes != 5 || stored[0].Sequence != 0 || stored[0].InFlightUploadBytes != 0 {
		t.Fatalf("stored lease = %+v", stored)
	}
	if err := connection.Close(); err == nil {
		t.Fatal("close succeeded before usage ACK")
	}
	pending := outbox.Pending()
	if len(pending) != 1 || pending[0].Reports[0].UploadedBytes != 5 || pending[0].Reports[0].Sequence != 1 {
		t.Fatalf("pending usage = %+v", pending)
	}
	if len(leases.Pending()) != 1 {
		t.Fatal("lease removed before ACK")
	}
	ack := agentproto.UsageAck{BatchID: pending[0].BatchID}
	ack.SHA256, err = agentproto.UsageBatchDigest(pending[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Acknowledge(ack); err != nil {
		t.Fatal(err)
	}
	if err := meter.(*RuntimeQuotaMeter).HandleUsageAck(ack.BatchID); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if quota.settle.LeaseID != quota.lease.ID || quota.settle.ConsumedBytes != 5 {
		t.Fatalf("settle = %+v", quota.settle)
	}
	if len(leases.Pending()) != 0 {
		t.Fatal("settled lease retained")
	}
}

func TestRuntimeQuotaMeterRejectsPartialUDPPacket(t *testing.T) {
	issued := time.Now().UTC().Truncate(time.Microsecond)
	quota := &meterQuotaStub{lease: agentruntime.QuotaLease{RequestID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", ConnectionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MultiplierMilli: 1000, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", GrantedBytes: 3, IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), PeriodEndsAt: issued.Add(time.Hour)}}
	dir := t.TempDir()
	outbox, _ := NewFileUsageOutbox(filepath.Join(dir, "usage.json"))
	defer outbox.Close()
	leases, _ := NewFileLeaseStore(filepath.Join(dir, "leases.json"))
	defer leases.Close()
	connection, err := NewRuntimeQuotaMeter(quota, outbox, leases).Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 1)
	if err != nil {
		t.Fatal(err)
	}
	var received bytes.Buffer
	if n, err := connection.WritePacket(&received, []byte("abcd"), agentmeter.Upload); !errors.Is(err, agentmeter.ErrExhausted) || n != 0 || received.Len() != 0 {
		t.Fatalf("packet = %d %v %q", n, err, received.String())
	}
	_ = connection.Close()
}

func TestRuntimeQuotaMeterRejectsInvalidGrant(t *testing.T) {
	quota := &meterQuotaStub{lease: agentruntime.QuotaLease{MultiplierMilli: 0}}
	dir := t.TempDir()
	outbox, _ := NewFileUsageOutbox(filepath.Join(dir, "usage.json"))
	defer outbox.Close()
	leases, _ := NewFileLeaseStore(filepath.Join(dir, "leases.json"))
	defer leases.Close()
	if _, err := NewRuntimeQuotaMeter(quota, outbox, leases).Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 1); err == nil {
		t.Fatal("invalid grant accepted")
	}
}

func TestRuntimeQuotaMeterOpensSelectedProxyLine(t *testing.T) {
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
	issued := time.Now().UTC().Truncate(time.Microsecond)
	quota := &meterQuotaStub{lease: agentruntime.QuotaLease{PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MultiplierMilli: 1000,
		ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", GrantedBytes: 8, IssuedAt: issued,
		ExpiresAt: issued.Add(time.Minute), PeriodEndsAt: issued.Add(time.Hour)}}
	meter := NewRuntimeQuotaMeter(quota, outbox, leases).(*RuntimeQuotaMeter)
	lineID := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	connection, err := meter.OpenLine(context.Background(), "proxy", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", lineID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if quota.open.LineID != lineID || quota.open.ResourceKind != "proxy" || len(leases.Pending()) != 1 {
		t.Fatalf("selected proxy line was not frozen: %+v", quota.open)
	}
	_ = connection.Close()
}

type blockingMeterWriter struct {
	ready   chan<- struct{}
	release <-chan struct{}
}

func (w blockingMeterWriter) Write(payload []byte) (int, error) {
	w.ready <- struct{}{}
	<-w.release
	return len(payload), nil
}

func TestRuntimeQuotaMeterTracksConcurrentUploadAndDownload(t *testing.T) {
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
	issued := time.Now().UTC().Truncate(time.Microsecond)
	quota := &meterQuotaStub{lease: agentruntime.QuotaLease{PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MultiplierMilli: 1000, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", GrantedBytes: 8, IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), PeriodEndsAt: issued.Add(time.Hour)}}
	connection, err := NewRuntimeQuotaMeter(quota, outbox, leases).Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 7)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		_, err := connection.Copy(blockingMeterWriter{ready, release}, bytes.NewBufferString("abcd"), agentmeter.Upload)
		results <- err
	}()
	go func() {
		_, err := connection.Copy(blockingMeterWriter{ready, release}, bytes.NewBufferString("xyz"), agentmeter.Download)
		results <- err
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			t.Fatal("both directions did not reach writer")
		}
	}
	state := leases.Pending()[0]
	if state.InFlightUploadBytes != 4 || state.InFlightDownloadBytes != 3 {
		t.Fatalf("in-flight = %+v", state)
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
	}
	state = leases.Pending()[0]
	if state.UploadedBytes != 4 || state.DownloadedBytes != 3 || state.ConsumedBytes != 7 || state.InFlightUploadBytes != 0 || state.InFlightDownloadBytes != 0 || state.Sequence != 0 {
		t.Fatalf("final state = %+v", state)
	}
	if len(outbox.Pending()) != 0 {
		t.Fatalf("per-write reports = %+v", outbox.Pending())
	}
}

func TestRuntimeQuotaMeterRejectsOpenWhenDurableReportCapacityIsReserved(t *testing.T) {
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
	quota := &meterQuotaStub{}
	meter := NewRuntimeQuotaMeter(quota, outbox, leases).(*RuntimeQuotaMeter)
	for i := 0; i < MaxPendingUsageBatches; i++ {
		meter.connections[fmt.Sprintf("connection-%d", i)] = &runtimeMeteredConnection{}
	}
	if _, err := meter.Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 1); !errors.Is(err, ErrUsageOutboxFull) {
		t.Fatalf("open at report capacity = %v", err)
	}
	if quota.open.ConnectionID != "" {
		t.Fatal("requested quota before reserving local report capacity")
	}
}

func TestRuntimeQuotaMeterCloseDuringPacketWriteFlushesWhenWriteFinishes(t *testing.T) {
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
	issued := time.Now().UTC().Truncate(time.Microsecond)
	quota := &meterQuotaStub{lease: agentruntime.QuotaLease{PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MultiplierMilli: 1000, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", GrantedBytes: 8, IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), PeriodEndsAt: issued.Add(time.Hour)}}
	meter := NewRuntimeQuotaMeter(quota, outbox, leases)
	connection, err := meter.Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 1)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := connection.WritePacket(blockingMeterWriter{ready, release}, []byte("abc"), agentmeter.Upload)
		done <- err
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("packet did not begin write")
	}
	if err := connection.Close(); !errors.Is(err, ErrLeaseWriteInFlight) {
		t.Fatalf("close during write = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	pending := outbox.Pending()
	if len(pending) != 1 || pending[0].Reports[0].UploadedBytes != 3 {
		t.Fatalf("write not flushed after close: %+v", pending)
	}
}
