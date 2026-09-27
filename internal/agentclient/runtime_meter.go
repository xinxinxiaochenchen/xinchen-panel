package agentclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"time"

	"controlplane/internal/agentmeter"
	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
	"controlplane/internal/metering"
	"controlplane/internal/platform/id"
)

var ErrUsagePending = errors.New("Agent usage acknowledgement is pending")
var ErrLeaseWriteInFlight = errors.New("Agent metered write is still in flight")

const runtimeQuotaRequestBytes = agentproto.MaxQuotaGrantBytes

type RuntimeQuotaMeter struct {
	mu          sync.Mutex
	quota       agentruntime.QuotaService
	outbox      *FileUsageOutbox
	leases      *FileLeaseStore
	connections map[string]*runtimeMeteredConnection
	recovered   map[string]struct{}
	opening     int
}

func NewRuntimeQuotaMeter(quota agentruntime.QuotaService, outbox *FileUsageOutbox, leases *FileLeaseStore) agentruntime.TrafficMeter {
	return &RuntimeQuotaMeter{quota: quota, outbox: outbox, leases: leases, connections: make(map[string]*runtimeMeteredConnection), recovered: make(map[string]struct{})}
}

func (m *RuntimeQuotaMeter) Open(ctx context.Context, resourceKind, resourceID string, revision uint64) (agentruntime.MeteredConnection, error) {
	return m.OpenLine(ctx, resourceKind, resourceID, "", revision)
}

func (m *RuntimeQuotaMeter) OpenLine(ctx context.Context, resourceKind, resourceID, lineID string, revision uint64) (agentruntime.MeteredConnection, error) {
	if m == nil || m.quota == nil || m.outbox == nil || m.leases == nil {
		return nil, errors.New("runtime quota meter is not configured")
	}
	m.mu.Lock()
	if len(m.connections)+len(m.recovered)+m.opening >= MaxPendingUsageBatches {
		m.mu.Unlock()
		return nil, ErrUsageOutboxFull
	}
	m.opening++
	m.mu.Unlock()
	reserved := true
	defer func() {
		if reserved {
			m.mu.Lock()
			m.opening--
			m.mu.Unlock()
		}
	}()
	connectionID, err := id.NewV7()
	if err != nil {
		return nil, err
	}
	requestID, err := id.NewV7()
	if err != nil {
		return nil, err
	}
	grant, err := m.quota.Open(ctx, agentruntime.QuotaOpenRequest{RequestID: requestID, ConnectionID: connectionID,
		ResourceKind: resourceKind, ResourceID: resourceID, LineID: lineID, Revision: int64(revision), RequestedBytes: runtimeQuotaRequestBytes})
	if err != nil {
		return nil, err
	}
	if grant.ConnectionID != connectionID || grant.RequestID != requestID || grant.ID == "" || grant.MultiplierMilli < 1 ||
		grant.MultiplierMilli > 100000 || grant.GrantedBytes < 1 || grant.GrantedBytes > runtimeQuotaRequestBytes ||
		grant.IssuedAt.IsZero() || grant.ExpiresAt.IsZero() || !grant.ExpiresAt.After(time.Now()) || grant.PeriodEndsAt.IsZero() || grant.ExpiresAt.After(grant.PeriodEndsAt) {
		return nil, errors.New("invalid Agent quota grant")
	}
	budget, err := agentmeter.NewBudget(grant.MultiplierMilli, grant.GrantedBytes, 0, 0, grant.ExpiresAt)
	if err != nil {
		return nil, err
	}
	state := LeaseState{ConnectionID: connectionID, LeaseID: grant.ID, ResourceKind: resourceKind, ResourceID: resourceID,
		Revision: int64(revision), PeriodID: grant.PeriodID, MultiplierMilli: grant.MultiplierMilli, GrantedBytes: grant.GrantedBytes,
		IssuedAt: grant.IssuedAt.UTC().Truncate(time.Microsecond), ExpiresAt: grant.ExpiresAt.UTC().Truncate(time.Microsecond), PeriodEndsAt: grant.PeriodEndsAt.UTC().Truncate(time.Microsecond)}
	if err := m.leases.Put(state); err != nil {
		return nil, err
	}
	connection := &runtimeMeteredConnection{owner: m, quota: m.quota, outbox: m.outbox, leases: m.leases, budget: budget, state: state, pending: make(map[string]struct{})}
	connection.changed = sync.NewCond(&connection.mu)
	m.mu.Lock()
	m.opening--
	m.connections[connectionID] = connection
	m.mu.Unlock()
	reserved = false
	return connection, nil
}

func (m *RuntimeQuotaMeter) HandleUsageAck(batchID string) error {
	m.mu.Lock()
	connections := make([]*runtimeMeteredConnection, 0, len(m.connections))
	for _, connection := range m.connections {
		connections = append(connections, connection)
	}
	m.mu.Unlock()
	for _, connection := range connections {
		if err := connection.handleAck(batchID); err != nil {
			return err
		}
	}
	return nil
}

type runtimeMeteredConnection struct {
	mu             sync.Mutex
	owner          *RuntimeQuotaMeter
	quota          agentruntime.QuotaService
	outbox         *FileUsageOutbox
	leases         *FileLeaseStore
	budget         *agentmeter.Budget
	state          LeaseState
	closed         bool
	closeRequested bool
	pending        map[string]struct{}
	settling       bool
	renewing       bool
	renewErr       error
	changed        *sync.Cond
	uploadMu       sync.Mutex
	downloadMu     sync.Mutex
}

func (c *runtimeMeteredConnection) Copy(dst io.Writer, src io.Reader, direction agentmeter.Direction) (int64, error) {
	if direction == agentmeter.Upload {
		c.uploadMu.Lock()
		defer c.uploadMu.Unlock()
	} else if direction == agentmeter.Download {
		c.downloadMu.Lock()
		defer c.downloadMu.Unlock()
	}
	if direction != agentmeter.Upload && direction != agentmeter.Download {
		return 0, agentmeter.ErrInvalidReservation
	}
	var copied int64
	var emptyReads int
	buffer := make([]byte, 16<<10)
	for {
		read, readErr := src.Read(buffer)
		if read < 0 || read > len(buffer) {
			return copied, io.ErrNoProgress
		}
		for offset := 0; offset < read; {
			c.mu.Lock()
			budget, leaseID := c.budget, c.state.LeaseID
			c.mu.Unlock()
			n, err := agentmeter.CopyMeteredWithJournal(dst, bytes.NewReader(buffer[offset:read]), budget, direction, leaseJournal{c, leaseID})
			offset += int(n)
			copied += n
			if errors.Is(err, agentmeter.ErrExhausted) || errors.Is(err, agentmeter.ErrExpired) {
				if err := c.renewLease(leaseID); err != nil {
					return copied, err
				}
				continue
			}
			if err != nil {
				return copied, err
			}
		}
		if readErr == io.EOF {
			return copied, nil
		}
		if readErr != nil {
			return copied, readErr
		}
		if read == 0 {
			emptyReads++
			if emptyReads >= 100 {
				return copied, io.ErrNoProgress
			}
		} else {
			emptyReads = 0
		}
	}
}

func (c *runtimeMeteredConnection) WritePacket(dst io.Writer, payload []byte, direction agentmeter.Direction) (int, error) {
	if direction == agentmeter.Upload {
		c.uploadMu.Lock()
		defer c.uploadMu.Unlock()
	} else if direction == agentmeter.Download {
		c.downloadMu.Lock()
		defer c.downloadMu.Unlock()
	}
	c.mu.Lock()
	budget, leaseID := c.budget, c.state.LeaseID
	c.mu.Unlock()
	return agentmeter.WritePacketWithJournal(dst, payload, budget, direction, leaseJournal{c, leaseID})
}

func (c *runtimeMeteredConnection) BeforeWrite(direction agentmeter.Direction, planned int64) error {
	return c.beforeWrite("", direction, planned)
}

type leaseJournal struct {
	connection *runtimeMeteredConnection
	leaseID    string
}

func (j leaseJournal) BeforeWrite(direction agentmeter.Direction, planned int64) error {
	return j.connection.beforeWrite(j.leaseID, direction, planned)
}

func (j leaseJournal) AfterWrite(direction agentmeter.Direction, planned, actual int64) error {
	return j.connection.afterWrite(j.leaseID, direction, planned, actual)
}

func (c *runtimeMeteredConnection) beforeWrite(leaseID string, direction agentmeter.Direction, planned int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.closeRequested {
		return errors.New("Agent metered connection closed")
	}
	if c.renewing || leaseID != "" && leaseID != c.state.LeaseID {
		return agentmeter.ErrExhausted
	}
	next := c.state
	if direction == agentmeter.Upload {
		next.InFlightUploadBytes = planned
		next.InFlightUploadBase = c.state.UploadedBytes
	} else if direction == agentmeter.Download {
		next.InFlightDownloadBytes = planned
		next.InFlightDownloadBase = c.state.DownloadedBytes
	} else {
		return agentmeter.ErrInvalidReservation
	}
	if err := c.leases.Put(next); err != nil {
		return err
	}
	c.state = next
	return nil
}

func (c *runtimeMeteredConnection) AfterWrite(direction agentmeter.Direction, planned, actual int64) error {
	return c.afterWrite("", direction, planned, actual)
}

func (c *runtimeMeteredConnection) afterWrite(leaseID string, direction agentmeter.Direction, planned, actual int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("Agent metered connection closed")
	}
	if leaseID != "" && leaseID != c.state.LeaseID {
		return ErrLeaseWriteInFlight
	}
	if (direction == agentmeter.Upload && c.state.InFlightUploadBytes != planned) || (direction == agentmeter.Download && c.state.InFlightDownloadBytes != planned) {
		return ErrLeaseWriteInFlight
	}
	next := c.state
	if direction == agentmeter.Upload {
		if actual > math.MaxInt64-next.UploadedBytes {
			return metering.ErrInvalid
		}
		next.UploadedBytes += actual
		next.InFlightUploadBytes = 0
		next.InFlightUploadBase = 0
	} else {
		if actual > math.MaxInt64-next.DownloadedBytes {
			return metering.ErrInvalid
		}
		next.DownloadedBytes += actual
		next.InFlightDownloadBytes = 0
		next.InFlightDownloadBase = 0
	}
	charged, err := chargedLeaseState(next)
	if err != nil {
		return err
	}
	previousCharged, err := chargedLeaseState(c.state)
	if err != nil {
		return err
	}
	next.ConsumedBytes += charged - previousCharged
	if err := c.leases.Put(next); err != nil {
		return err
	}
	c.state = next
	c.changed.Broadcast()
	if c.closeRequested && c.state.InFlightUploadBytes == 0 && c.state.InFlightDownloadBytes == 0 && len(c.pending) == 0 {
		if c.state.UploadedBytes != c.state.ReportedUploadedBytes || c.state.DownloadedBytes != c.state.ReportedDownloadedBytes {
			if err := c.enqueueReportLocked(); err != nil {
				return err
			}
		} else if err := c.settleLocked(); err != nil {
			return err
		}
	}
	return nil
}

func (c *runtimeMeteredConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	if c.state.InFlightUploadBytes != 0 || c.state.InFlightDownloadBytes != 0 {
		c.closeRequested = true
		c.changed.Broadcast()
		return ErrLeaseWriteInFlight
	}
	if len(c.pending) > 0 || c.hasPendingOutbox() {
		c.closeRequested = true
		c.changed.Broadcast()
		return ErrUsagePending
	}
	if c.state.UploadedBytes != c.state.ReportedUploadedBytes || c.state.DownloadedBytes != c.state.ReportedDownloadedBytes {
		if err := c.enqueueReportLocked(); err != nil {
			return err
		}
		c.closeRequested = true
		c.changed.Broadcast()
		return ErrUsagePending
	}
	return c.settleLocked()
}

func (c *runtimeMeteredConnection) enqueueReportLocked() error {
	if c.state.UploadedBytes == c.state.ReportedUploadedBytes && c.state.DownloadedBytes == c.state.ReportedDownloadedBytes {
		return nil
	}
	sequence := c.state.Sequence + 1
	batchID, err := id.NewV7()
	if err != nil {
		return err
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	if observed.After(c.state.ExpiresAt) {
		observed = c.state.ExpiresAt
	}
	batch := agentproto.UsageBatch{BatchID: batchID, Reports: []agentproto.UsageReport{{ConnectionID: c.state.ConnectionID, LeaseID: c.state.LeaseID, Sequence: sequence, UploadedBytes: c.state.UploadedBytes, DownloadedBytes: c.state.DownloadedBytes, ObservedAt: observed}}}
	if err := c.outbox.Enqueue(batch); err != nil {
		return err
	}
	c.pending[batchID] = struct{}{}
	c.state.Sequence = sequence
	c.state.ReportedUploadedBytes = c.state.UploadedBytes
	c.state.ReportedDownloadedBytes = c.state.DownloadedBytes
	return c.leases.Put(c.state)
}

func (c *runtimeMeteredConnection) hasPendingOutbox() bool {
	for _, batch := range c.outbox.Pending() {
		for _, report := range batch.Reports {
			if report.ConnectionID == c.state.ConnectionID && report.LeaseID == c.state.LeaseID {
				return true
			}
		}
	}
	return false
}

func (c *runtimeMeteredConnection) handleAck(batchID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.pending[batchID]; !exists {
		return nil
	}
	delete(c.pending, batchID)
	c.changed.Broadcast()
	if !c.closeRequested || len(c.pending) > 0 || c.hasPendingOutbox() || c.closed || c.settling {
		return nil
	}
	c.settling = true
	if err := c.settleLocked(); err != nil {
		c.settling = false
		return err
	}
	return nil
}

// renewLease holds back both traffic directions until every write under the
// old lease has been checkpointed and its final usage report is acknowledged.
func (c *runtimeMeteredConnection) renewLease(leaseID string) error {
	c.mu.Lock()
	for c.renewing && c.state.LeaseID == leaseID && !c.closed && !c.closeRequested {
		c.changed.Wait()
	}
	if c.closed || c.closeRequested {
		c.mu.Unlock()
		return agentmeter.ErrExhausted
	}
	if c.state.LeaseID != leaseID {
		c.mu.Unlock()
		return nil
	}
	if c.renewErr != nil {
		err := c.renewErr
		c.mu.Unlock()
		return err
	}
	c.renewing = true
	defer func() {
		c.renewing = false
		c.changed.Broadcast()
		c.mu.Unlock()
	}()
	for c.state.InFlightUploadBytes != 0 || c.state.InFlightDownloadBytes != 0 {
		c.changed.Wait()
	}
	if c.state.UploadedBytes != c.state.ReportedUploadedBytes || c.state.DownloadedBytes != c.state.ReportedDownloadedBytes {
		if err := c.enqueueReportLocked(); err != nil {
			c.renewErr = err
			return err
		}
	}
	for !c.closeRequested && (len(c.pending) > 0 || c.hasPendingOutbox()) {
		c.changed.Wait()
	}
	if c.closeRequested {
		return agentmeter.ErrExhausted
	}
	requestID, err := id.NewV7()
	if err != nil {
		c.renewErr = err
		return err
	}
	old := c.state
	_, err = c.quota.Settle(context.Background(), agentruntime.QuotaSettleRequest{
		RequestID: requestID, ConnectionID: old.ConnectionID, LeaseID: old.LeaseID, ConsumedBytes: old.ConsumedBytes,
	})
	if err != nil {
		c.renewErr = err
		return err
	}
	if err := c.leases.Delete(old.ConnectionID, old.LeaseID); err != nil {
		c.renewErr = err
		return err
	}
	requestID, err = id.NewV7()
	if err != nil {
		c.renewErr = err
		return err
	}
	grant, err := c.quota.Renew(context.Background(), agentruntime.QuotaRenewRequest{
		RequestID: requestID, ConnectionID: old.ConnectionID, Revision: old.Revision, RequestedBytes: runtimeQuotaRequestBytes,
	})
	if err != nil {
		c.renewErr = err
		return err
	}
	if grant.RequestID != requestID || grant.ConnectionID != old.ConnectionID || grant.ID == "" || grant.ID == old.LeaseID ||
		grant.PeriodID != old.PeriodID || grant.MultiplierMilli != old.MultiplierMilli || grant.GrantedBytes < 1 ||
		grant.GrantedBytes > runtimeQuotaRequestBytes || grant.ConsumedBytes != 0 || grant.IssuedAt.IsZero() ||
		grant.ExpiresAt.IsZero() || !grant.ExpiresAt.After(time.Now()) || grant.PeriodEndsAt.IsZero() ||
		grant.ExpiresAt.After(grant.PeriodEndsAt) || !grant.PeriodEndsAt.Equal(old.PeriodEndsAt) {
		c.renewErr = errors.New("invalid Agent quota renewal")
		return c.renewErr
	}
	budget, err := agentmeter.NewBudget(grant.MultiplierMilli, grant.GrantedBytes, old.UploadedBytes, old.DownloadedBytes, grant.ExpiresAt)
	if err != nil {
		c.renewErr = err
		return err
	}
	next := old
	next.LeaseID, next.GrantedBytes, next.ConsumedBytes = grant.ID, grant.GrantedBytes, 0
	next.IssuedAt, next.ExpiresAt = grant.IssuedAt.UTC().Truncate(time.Microsecond), grant.ExpiresAt.UTC().Truncate(time.Microsecond)
	if err := c.leases.Put(next); err != nil {
		c.renewErr = err
		return err
	}
	c.state, c.budget = next, budget
	c.renewErr = nil
	return nil
}

func (c *runtimeMeteredConnection) settleLocked() error {
	if c.closed {
		return nil
	}
	requestID, err := id.NewV7()
	if err != nil {
		return err
	}
	if _, err := c.quota.Settle(context.Background(), agentruntime.QuotaSettleRequest{RequestID: requestID, ConnectionID: c.state.ConnectionID, LeaseID: c.state.LeaseID, ConsumedBytes: c.state.ConsumedBytes}); err != nil {
		return err
	}
	if err := c.leases.Delete(c.state.ConnectionID, c.state.LeaseID); err != nil {
		return err
	}
	c.closed = true
	if c.owner != nil {
		c.owner.mu.Lock()
		delete(c.owner.connections, c.state.ConnectionID)
		c.owner.mu.Unlock()
	}
	return nil
}
