package agentclient

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"controlplane/internal/agentmeter"
	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
)

type renewingQuotaStub struct {
	first   agentruntime.QuotaLease
	second  agentruntime.QuotaLease
	settled chan agentruntime.QuotaSettleRequest
	renewed chan agentruntime.QuotaRenewRequest
}

type blockedFailedWriter struct {
	ready   chan<- struct{}
	release <-chan struct{}
}

func (w blockedFailedWriter) Write([]byte) (int, error) {
	w.ready <- struct{}{}
	<-w.release
	return 0, io.ErrClosedPipe
}

func TestRuntimeQuotaMeterCloseWhileRenewalWaitsForWriteDoesNotRenew(t *testing.T) {
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
	q := &renewingQuotaStub{settled: make(chan agentruntime.QuotaSettleRequest, 2), renewed: make(chan agentruntime.QuotaRenewRequest, 1),
		first: agentruntime.QuotaLease{PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MultiplierMilli: 1000, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", GrantedBytes: 3, IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), PeriodEndsAt: issued.Add(time.Hour)}}
	meter := NewRuntimeQuotaMeter(q, outbox, leases).(*RuntimeQuotaMeter)
	opened, err := meter.Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 1)
	if err != nil {
		t.Fatal(err)
	}
	c := opened.(*runtimeMeteredConnection)
	ready, release := make(chan struct{}, 1), make(chan struct{})
	results := make(chan error, 2)
	go func() {
		_, err := c.Copy(blockedFailedWriter{ready, release}, bytes.NewBufferString("abc"), agentmeter.Download)
		results <- err
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("write did not start")
	}
	go func() {
		_, err := c.Copy(io.Discard, bytes.NewBufferString("x"), agentmeter.Upload)
		results <- err
	}()
	deadline := time.After(3 * time.Second)
	for {
		c.mu.Lock()
		renewing := c.renewing
		c.mu.Unlock()
		if renewing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("renewal did not wait for write")
		case <-time.After(time.Millisecond):
		}
	}
	if err := c.Close(); err != ErrLeaseWriteInFlight {
		t.Fatalf("close during write = %v", err)
	}
	close(release)
	for range 2 {
		select {
		case <-results:
		case <-time.After(3 * time.Second):
			t.Fatal("copy hung after close")
		}
	}
	select {
	case <-q.renewed:
		t.Fatal("closed connection renewed")
	default:
	}
}

func (q *renewingQuotaStub) Open(_ context.Context, req agentruntime.QuotaOpenRequest) (agentruntime.QuotaLease, error) {
	grant := q.first
	grant.RequestID, grant.ConnectionID = req.RequestID, req.ConnectionID
	return grant, nil
}
func (q *renewingQuotaStub) Renew(_ context.Context, req agentruntime.QuotaRenewRequest) (agentruntime.QuotaLease, error) {
	q.renewed <- req
	grant := q.second
	grant.RequestID, grant.ConnectionID = req.RequestID, req.ConnectionID
	return grant, nil
}
func (q *renewingQuotaStub) Settle(_ context.Context, req agentruntime.QuotaSettleRequest) (agentruntime.QuotaSettlement, error) {
	q.settled <- req
	return agentruntime.QuotaSettlement{RequestID: req.RequestID, ConnectionID: req.ConnectionID, LeaseID: req.LeaseID, ConsumedBytes: req.ConsumedBytes}, nil
}

func TestRuntimeQuotaMeterRenewsAfterAckWithoutDroppingBufferedTCPBytes(t *testing.T) {
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
	q := &renewingQuotaStub{settled: make(chan agentruntime.QuotaSettleRequest, 2), renewed: make(chan agentruntime.QuotaRenewRequest, 1),
		first:  agentruntime.QuotaLease{PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MultiplierMilli: 1000, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", GrantedBytes: 3, IssuedAt: issued, ExpiresAt: issued.Add(30 * time.Second), PeriodEndsAt: issued.Add(time.Hour)},
		second: agentruntime.QuotaLease{PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MultiplierMilli: 1000, ID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", GrantedBytes: 3, IssuedAt: issued, ExpiresAt: issued.Add(30 * time.Second), PeriodEndsAt: issued.Add(time.Hour)}}
	meter := NewRuntimeQuotaMeter(q, outbox, leases).(*RuntimeQuotaMeter)
	connection, err := meter.Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 1)
	if err != nil {
		t.Fatal(err)
	}
	var destination bytes.Buffer
	result := make(chan error, 1)
	go func() {
		_, err := connection.Copy(&destination, bytes.NewBufferString("abcdef"), agentmeter.Upload)
		result <- err
	}()
	deadline := time.After(3 * time.Second)
	var batch agentproto.UsageBatch
	for {
		pending := outbox.Pending()
		if len(pending) > 0 {
			batch = pending[0]
			break
		}
		select {
		case <-deadline:
			t.Fatal("renewal never reported first lease")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if destination.String() != "abc" || batch.Reports[0].UploadedBytes != 3 || batch.Reports[0].LeaseID != q.first.ID {
		t.Fatalf("first lease = %q %+v", destination.String(), batch)
	}
	select {
	case <-q.renewed:
		t.Fatal("renewed before usage ACK")
	default:
	}
	ack := agentproto.UsageAck{BatchID: batch.BatchID}
	ack.SHA256, err = agentproto.UsageBatchDigest(batch)
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Acknowledge(ack); err != nil {
		t.Fatal(err)
	}
	if err := meter.HandleUsageAck(batch.BatchID); err != nil {
		t.Fatal(err)
	}
	select {
	case settled := <-q.settled:
		if settled.LeaseID != q.first.ID || settled.ConsumedBytes != 3 {
			t.Fatalf("first settlement = %+v", settled)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first lease not settled")
	}
	select {
	case <-q.renewed:
	case <-time.After(3 * time.Second):
		t.Fatal("next lease not requested")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("copy did not resume")
	}
	if destination.String() != "abcdef" {
		t.Fatalf("buffered bytes lost: %q", destination.String())
	}
	if err := connection.Close(); err != ErrUsagePending {
		t.Fatalf("close = %v", err)
	}
	second := outbox.Pending()
	if len(second) != 1 || second[0].Reports[0].Sequence != 2 || second[0].Reports[0].UploadedBytes != 6 || second[0].Reports[0].LeaseID != q.second.ID {
		t.Fatalf("second report = %+v", second)
	}
	ack = agentproto.UsageAck{BatchID: second[0].BatchID}
	ack.SHA256, err = agentproto.UsageBatchDigest(second[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Acknowledge(ack); err != nil {
		t.Fatal(err)
	}
	if err := meter.HandleUsageAck(ack.BatchID); err != nil {
		t.Fatal(err)
	}
	select {
	case settled := <-q.settled:
		if settled.LeaseID != q.second.ID || settled.ConsumedBytes != 3 {
			t.Fatalf("second settlement = %+v", settled)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second lease not settled")
	}
}

func TestRuntimeQuotaMeterCloseDuringExhaustionReportsWriteAndDoesNotRenew(t *testing.T) {
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
	q := &renewingQuotaStub{settled: make(chan agentruntime.QuotaSettleRequest, 1), renewed: make(chan agentruntime.QuotaRenewRequest, 1),
		first: agentruntime.QuotaLease{PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MultiplierMilli: 1000, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", GrantedBytes: 3, IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), PeriodEndsAt: issued.Add(time.Hour)}}
	meter := NewRuntimeQuotaMeter(q, outbox, leases).(*RuntimeQuotaMeter)
	connection, err := meter.Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 1)
	if err != nil {
		t.Fatal(err)
	}
	ready, release := make(chan struct{}, 1), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := connection.Copy(blockingMeterWriter{ready, release}, bytes.NewBufferString("abcdef"), agentmeter.Upload)
		result <- err
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("first write did not start")
	}
	if err := connection.Close(); err != ErrLeaseWriteInFlight {
		t.Fatalf("close during write = %v", err)
	}
	close(release)
	select {
	case <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("copy did not stop after close")
	}
	pending := outbox.Pending()
	if len(pending) != 1 || pending[0].Reports[0].UploadedBytes != 3 {
		t.Fatalf("interrupted close report = %+v", pending)
	}
	select {
	case <-q.renewed:
		t.Fatal("closed connection renewed")
	default:
	}
}

func TestRuntimeQuotaMeterCloseCancelsRenewalWaitingForAck(t *testing.T) {
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
	q := &renewingQuotaStub{settled: make(chan agentruntime.QuotaSettleRequest, 1), renewed: make(chan agentruntime.QuotaRenewRequest, 1),
		first: agentruntime.QuotaLease{PeriodID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", MultiplierMilli: 1000, ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", GrantedBytes: 3, IssuedAt: issued, ExpiresAt: issued.Add(time.Minute), PeriodEndsAt: issued.Add(time.Hour)}}
	meter := NewRuntimeQuotaMeter(q, outbox, leases).(*RuntimeQuotaMeter)
	connection, err := meter.Open(context.Background(), "forward", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 1)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := connection.Copy(io.Discard, bytes.NewBufferString("abcdef"), agentmeter.Upload)
		result <- err
	}()
	deadline := time.After(3 * time.Second)
	for len(outbox.Pending()) == 0 {
		select {
		case <-deadline:
			t.Fatal("first lease not reported")
		case <-time.After(time.Millisecond):
		}
	}
	if err := connection.Close(); err != ErrUsagePending {
		t.Fatalf("close while awaiting ACK = %v", err)
	}
	select {
	case <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("renewal did not stop after connection close")
	}
	select {
	case <-q.renewed:
		t.Fatal("closed connection renewed")
	default:
	}
}
