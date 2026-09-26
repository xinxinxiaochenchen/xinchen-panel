package agentclient

import (
	"context"
	"errors"
	"math"
	"time"

	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
	"controlplane/internal/metering"
	"controlplane/internal/platform/id"
)

// PrepareRecovery reconstructs reports for writes interrupted between a
// durable intent and the final checkpoint. It must run before new listeners
// are opened. A pending outbox report takes precedence over stale lease state.
func (m *RuntimeQuotaMeter) PrepareRecovery() error {
	if m == nil || m.outbox == nil || m.leases == nil {
		return errors.New("runtime quota meter is not configured")
	}
	states := m.leases.Pending()
	for _, state := range states {
		m.mu.Lock()
		m.recovered[leaseKey(state.ConnectionID, state.LeaseID)] = struct{}{}
		m.mu.Unlock()
		var newest *agentproto.UsageReport
		for _, batch := range m.outbox.Pending() {
			for _, report := range batch.Reports {
				if report.ConnectionID == state.ConnectionID && report.LeaseID == state.LeaseID &&
					(newest == nil || report.Sequence > newest.Sequence) {
					copy := report
					newest = &copy
				}
			}
		}
		if newest != nil && newest.Sequence > state.Sequence {
			if newest.UploadedBytes < state.UploadedBytes || newest.DownloadedBytes < state.DownloadedBytes {
				return errors.New("recovered usage counters moved backwards")
			}
			previousCharged, err := chargedLeaseState(state)
			if err != nil {
				return err
			}
			uploadBase, downloadBase := state.InFlightUploadBase, state.InFlightDownloadBase
			uploadIntent, downloadIntent := state.InFlightUploadBytes, state.InFlightDownloadBytes
			state.UploadedBytes = newest.UploadedBytes
			state.DownloadedBytes = newest.DownloadedBytes
			state.ReportedUploadedBytes = newest.UploadedBytes
			state.ReportedDownloadedBytes = newest.DownloadedBytes
			state.Sequence = newest.Sequence
			if newest.UploadedBytes >= uploadBase+uploadIntent {
				state.InFlightUploadBytes, state.InFlightUploadBase = 0, 0
			}
			if newest.DownloadedBytes >= downloadBase+downloadIntent {
				state.InFlightDownloadBytes, state.InFlightDownloadBase = 0, 0
			}
			charged, err := chargedLeaseState(state)
			if err != nil {
				return err
			}
			if err := addRecoveredCharge(&state, previousCharged, charged); err != nil {
				return err
			}
			if err := m.leases.Put(state); err != nil {
				return err
			}
		}
		if state.InFlightUploadBytes == 0 && state.InFlightDownloadBytes == 0 {
			if state.UploadedBytes != state.ReportedUploadedBytes || state.DownloadedBytes != state.ReportedDownloadedBytes {
				if err := m.enqueueRecoveredReport(&state); err != nil {
					return err
				}
			}
			continue
		}
		uploadMissing := missingInFlight(state.InFlightUploadBytes, state.InFlightUploadBase, state.UploadedBytes)
		downloadMissing := missingInFlight(state.InFlightDownloadBytes, state.InFlightDownloadBase, state.DownloadedBytes)
		previousCharged, err := chargedLeaseState(state)
		if err != nil {
			return err
		}
		if state.UploadedBytes > math.MaxInt64-uploadMissing || state.DownloadedBytes > math.MaxInt64-downloadMissing {
			return errors.New("recovered usage overflow")
		}
		state.UploadedBytes += uploadMissing
		state.DownloadedBytes += downloadMissing
		charged, err := chargedLeaseState(state)
		if err != nil {
			return err
		}
		if err := addRecoveredCharge(&state, previousCharged, charged); err != nil {
			return err
		}
		state.InFlightUploadBytes = 0
		state.InFlightDownloadBytes = 0
		state.InFlightUploadBase = 0
		state.InFlightDownloadBase = 0
		if err := m.enqueueRecoveredReport(&state); err != nil {
			return err
		}
	}
	return nil
}

func addRecoveredCharge(state *LeaseState, before, after int64) error {
	if after < before || state.ConsumedBytes > math.MaxInt64-(after-before) {
		return metering.ErrInvalid
	}
	state.ConsumedBytes += after - before
	return nil
}

func (m *RuntimeQuotaMeter) enqueueRecoveredReport(state *LeaseState) error {
	batchID, err := id.NewV7()
	if err != nil {
		return err
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	if observed.After(state.ExpiresAt) {
		observed = state.ExpiresAt
	}
	if observed.Before(state.IssuedAt) {
		observed = state.IssuedAt
	}
	state.Sequence++
	state.ReportedUploadedBytes = state.UploadedBytes
	state.ReportedDownloadedBytes = state.DownloadedBytes
	batch := agentproto.UsageBatch{BatchID: batchID, Reports: []agentproto.UsageReport{{ConnectionID: state.ConnectionID, LeaseID: state.LeaseID,
		Sequence: state.Sequence, UploadedBytes: state.UploadedBytes, DownloadedBytes: state.DownloadedBytes, ObservedAt: observed}}}
	if err := m.outbox.Enqueue(batch); err != nil {
		return err
	}
	return m.leases.Put(*state)
}

func missingInFlight(planned, base, current int64) int64 {
	if planned == 0 {
		return 0
	}
	accounted := current - base
	if accounted >= planned {
		return 0
	}
	return planned - accounted
}

func chargedLeaseState(state LeaseState) (int64, error) {
	if state.UploadedBytes > math.MaxInt64-state.DownloadedBytes {
		return 0, metering.ErrInvalid
	}
	return metering.Charged(state.UploadedBytes+state.DownloadedBytes, state.MultiplierMilli)
}

// SettleRecovered runs only after the outbox has replayed and ACKed every
// report for a recovered connection. Active connections opened in this runtime
// are deliberately excluded.
func (m *RuntimeQuotaMeter) SettleRecovered(ctx context.Context) error {
	if m == nil || m.quota == nil || m.outbox == nil || m.leases == nil {
		return errors.New("runtime quota meter is not configured")
	}
	states := m.leases.Pending()
	for _, state := range states {
		key := leaseKey(state.ConnectionID, state.LeaseID)
		m.mu.Lock()
		_, recovering := m.recovered[key]
		m.mu.Unlock()
		if !recovering {
			continue
		}
		for _, batch := range m.outbox.Pending() {
			for _, report := range batch.Reports {
				if report.ConnectionID == state.ConnectionID && report.LeaseID == state.LeaseID {
					return ErrUsagePending
				}
			}
		}
		if state.InFlightUploadBytes != 0 || state.InFlightDownloadBytes != 0 {
			return ErrLeaseWriteInFlight
		}
		requestID, err := id.NewV7()
		if err != nil {
			return err
		}
		if _, err := m.quota.Settle(ctx, agentruntime.QuotaSettleRequest{RequestID: requestID,
			ConnectionID: state.ConnectionID, LeaseID: state.LeaseID, ConsumedBytes: state.ConsumedBytes}); err != nil {
			return err
		}
		if err := m.leases.Delete(state.ConnectionID, state.LeaseID); err != nil {
			return err
		}
		m.mu.Lock()
		delete(m.recovered, key)
		m.mu.Unlock()
	}
	return nil
}
