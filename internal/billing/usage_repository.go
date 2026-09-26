package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

func sameConnection(a, b Connection) bool {
	return sameID(a.PeriodID, b.PeriodID) && sameID(a.AgentID, b.AgentID) && sameID(a.IngressNodeID, b.IngressNodeID) && sameID(a.LineID, b.LineID) && a.MultiplierMilli == b.MultiplierMilli && a.StartedAt.Equal(b.StartedAt) && sameID(a.LeaseID, b.LeaseID)
}

// RegisterConnection is an internal accounting primitive. Its caller must map
// the authenticated proxy credential to the connection before invoking it.
// The database verifies the Agent/ingress binding and frozen multiplier.
func (r *PostgresRepository) RegisterConnection(ctx context.Context, input Connection) error {
	if !uuidPattern.MatchString(input.ID) || !uuidPattern.MatchString(input.LeaseID) || !uuidPattern.MatchString(input.PeriodID) || !uuidPattern.MatchString(input.AgentID) || !uuidPattern.MatchString(input.IngressNodeID) || input.StartedAt.IsZero() {
		return ErrNotFound
	}
	input.StartedAt = input.StartedAt.UTC().Truncate(time.Microsecond)
	var lineArg any
	if input.LineID != "" {
		if !uuidPattern.MatchString(input.LineID) {
			return ErrNotFound
		}
		lineArg = input.LineID
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockPeriod(ctx, tx, input.PeriodID); err != nil {
		return err
	}
	var existing Connection
	var existingLine *string
	err = tx.QueryRow(ctx, `SELECT billing_period_id::text,agent_id::text,ingress_node_id::text,line_id::text,multiplier_milli,started_at,first_lease_id::text
 FROM usage_sessions WHERE id=$1 FOR UPDATE`, input.ID).Scan(&existing.PeriodID, &existing.AgentID, &existing.IngressNodeID, &existingLine, &existing.MultiplierMilli, &existing.StartedAt, &existing.LeaseID)
	if err == nil {
		if existingLine != nil {
			existing.LineID = *existingLine
		}
		if !sameConnection(existing, input) {
			return ErrConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return databaseError(err)
	}
	var userID string
	var lease Lease
	lease, err = scanLease(tx.QueryRow(ctx, `SELECT `+leaseColumns+` FROM quota_leases WHERE id=$1 AND agent_id=$2 AND billing_period_id=$3`, input.LeaseID, input.AgentID, input.PeriodID))
	if err != nil {
		return err
	}
	if lease.State != "active" || input.StartedAt.Before(lease.IssuedAt) || !input.StartedAt.Before(lease.ExpiresAt) {
		return ErrLeaseClosed
	}
	var effective int64
	err = tx.QueryRow(ctx, `SELECT p.user_id::text,COALESCE(l.multiplier_milli,n.multiplier_milli,(p.snapshot_json->>'default_multiplier_milli')::integer)
 FROM billing_periods p JOIN agents a ON a.id=$2 JOIN nodes n ON n.id=a.node_id
 LEFT JOIN lines l ON l.id=$4
 WHERE p.id=$1 AND n.id=$3 AND a.status='online' AND n.enabled
 AND p.starts_at<=$5 AND p.ends_at>$5
 AND ($4::uuid IS NULL OR (l.enabled AND EXISTS(SELECT 1 FROM line_hops h WHERE h.line_id=l.id AND h.position=0 AND h.node_id=n.id)))`, input.PeriodID, input.AgentID, input.IngressNodeID, lineArg, input.StartedAt).Scan(&userID, &effective)
	if err != nil {
		return fmt.Errorf("validate usage connection: %w", databaseError(err))
	}
	if effective != input.MultiplierMilli {
		return ErrConflict
	}
	tag, err := tx.Exec(ctx, `INSERT INTO usage_sessions(id,billing_period_id,user_id,agent_id,ingress_node_id,line_id,multiplier_milli,started_at,first_lease_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (id) DO NOTHING`, input.ID, input.PeriodID, userID, input.AgentID, input.IngressNodeID, lineArg, input.MultiplierMilli, input.StartedAt, input.LeaseID)
	if err != nil {
		return fmt.Errorf("register usage connection: %w", databaseError(err))
	}
	if tag.RowsAffected() == 0 {
		err = tx.QueryRow(ctx, `SELECT billing_period_id::text,agent_id::text,ingress_node_id::text,line_id::text,multiplier_milli,started_at,first_lease_id::text
        FROM usage_sessions WHERE id=$1 FOR UPDATE`, input.ID).Scan(&existing.PeriodID, &existing.AgentID, &existing.IngressNodeID, &existingLine, &existing.MultiplierMilli, &existing.StartedAt, &existing.LeaseID)
		if err != nil {
			return databaseError(err)
		}
		if existingLine != nil {
			existing.LineID = *existingLine
		}
		if !sameConnection(existing, input) {
			return ErrConflict
		}
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) RecordUsage(ctx context.Context, agentID string, report UsageReport) (UsageEvent, error) {
	if !uuidPattern.MatchString(agentID) || !uuidPattern.MatchString(report.ConnectionID) || !uuidPattern.MatchString(report.LeaseID) {
		return UsageEvent{}, ErrNotFound
	}
	if report.Sequence < 1 || report.ObservedAt.IsZero() {
		return UsageEvent{}, ErrInvalidMeter
	}
	report.ObservedAt = report.ObservedAt.UTC().Truncate(time.Microsecond)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return UsageEvent{}, err
	}
	defer tx.Rollback(ctx)
	var lockPeriodID string
	err = tx.QueryRow(ctx, `SELECT billing_period_id::text FROM usage_sessions WHERE id=$1 AND agent_id=$2`, report.ConnectionID, agentID).Scan(&lockPeriodID)
	if err != nil {
		return UsageEvent{}, databaseError(err)
	}
	if err := lockPeriod(ctx, tx, lockPeriodID); err != nil {
		return UsageEvent{}, err
	}
	var periodID, userID, nodeID string
	var lineID, resourceKind *string
	var multiplier, lastSequence int64
	var before Counters
	var startedAt, periodEndsAt time.Time
	var lastObservedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT s.billing_period_id::text,s.user_id::text,s.ingress_node_id::text,s.line_id::text,s.resource_kind,s.multiplier_milli,s.last_sequence,s.uploaded_bytes,s.downloaded_bytes,s.started_at,s.last_observed_at,p.ends_at
 FROM usage_sessions s JOIN billing_periods p ON p.id=s.billing_period_id WHERE s.id=$1 AND s.agent_id=$2 FOR UPDATE OF s`, report.ConnectionID, agentID).Scan(&periodID, &userID, &nodeID, &lineID, &resourceKind, &multiplier, &lastSequence, &before.UploadedBytes, &before.DownloadedBytes, &startedAt, &lastObservedAt, &periodEndsAt)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("load usage connection: %w", databaseError(err))
	}
	if resourceKind != nil {
		var belongs bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM connection_lease_bindings WHERE connection_id=$1 AND lease_id=$2)`, report.ConnectionID, report.LeaseID).Scan(&belongs); err != nil {
			return UsageEvent{}, databaseError(err)
		}
		if !belongs {
			return UsageEvent{}, ErrNotFound
		}
	}
	if !validUsageObservation(startedAt, periodEndsAt, report.ObservedAt) {
		return UsageEvent{}, ErrInvalidMeter
	}
	if report.Sequence <= lastSequence {
		var prior UsageEvent
		var priorObservedAt time.Time
		var priorLeaseID *string
		err = tx.QueryRow(ctx, `SELECT id::text,cumulative_uploaded_bytes,cumulative_downloaded_bytes,uploaded_bytes,downloaded_bytes,charged_bytes,observed_at,lease_id::text
  FROM usage_events WHERE connection_id=$1 AND sequence=$2`, report.ConnectionID, report.Sequence).Scan(&prior.ID, &prior.Cumulative.UploadedBytes, &prior.Cumulative.DownloadedBytes, &prior.UploadedBytes, &prior.DownloadedBytes, &prior.ChargedBytes, &priorObservedAt, &priorLeaseID)
		if err != nil {
			return UsageEvent{}, databaseError(err)
		}
		if priorLeaseID == nil || !sameID(*priorLeaseID, report.LeaseID) || prior.Cumulative != report.Counters || !priorObservedAt.Equal(report.ObservedAt) {
			return UsageEvent{}, ErrConflict
		}
		prior.ConnectionID = report.ConnectionID
		prior.Sequence = report.Sequence
		return prior, tx.Commit(ctx)
	}
	if report.Sequence != lastSequence+1 {
		return UsageEvent{}, ErrSequence
	}
	if lastObservedAt != nil && report.ObservedAt.Before(*lastObservedAt) {
		return UsageEvent{}, ErrInvalidMeter
	}
	delta, err := ChargeDelta(before, report.Counters, multiplier)
	if err != nil {
		return UsageEvent{}, err
	}
	reservedUsed, err := consumeLease(ctx, tx, agentID, periodID, report.LeaseID, delta.ChargedBytes, report.ObservedAt)
	if err != nil {
		return UsageEvent{}, err
	}
	eventID, err := id.NewV7()
	if err != nil {
		return UsageEvent{}, err
	}
	var lineArg any
	if lineID != nil {
		lineArg = *lineID
	}
	_, err = tx.Exec(ctx, `INSERT INTO usage_events(id,connection_id,billing_period_id,user_id,ingress_node_id,line_id,sequence,
 cumulative_uploaded_bytes,cumulative_downloaded_bytes,uploaded_bytes,downloaded_bytes,multiplier_milli,charged_bytes,observed_at,lease_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, eventID, report.ConnectionID, periodID, userID, nodeID, lineArg, report.Sequence,
		report.Counters.UploadedBytes, report.Counters.DownloadedBytes, delta.UploadedBytes, delta.DownloadedBytes, multiplier, delta.ChargedBytes, report.ObservedAt, report.LeaseID)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("append usage event: %w", databaseError(err))
	}
	_, err = tx.Exec(ctx, `UPDATE usage_sessions SET last_sequence=$2,uploaded_bytes=$3,downloaded_bytes=$4,charged_bytes=charged_bytes+$5,last_observed_at=$6 WHERE id=$1`, report.ConnectionID, report.Sequence, report.Counters.UploadedBytes, report.Counters.DownloadedBytes, delta.ChargedBytes, report.ObservedAt)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("advance usage session: %w", databaseError(err))
	}
	_, err = tx.Exec(ctx, `UPDATE billing_periods SET uploaded_bytes=uploaded_bytes+$2,downloaded_bytes=downloaded_bytes+$3,charged_bytes=charged_bytes+$4,reserved_bytes=reserved_bytes-$5 WHERE id=$1`, periodID, delta.UploadedBytes, delta.DownloadedBytes, delta.ChargedBytes, reservedUsed)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("charge billing period: %w", databaseError(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return UsageEvent{}, databaseError(err)
	}
	return UsageEvent{ID: eventID, ConnectionID: report.ConnectionID, Sequence: report.Sequence, Cumulative: report.Counters, UploadedBytes: delta.UploadedBytes, DownloadedBytes: delta.DownloadedBytes, ChargedBytes: delta.ChargedBytes}, nil
}

// The period admits new connections in [start,end), while the final
// cumulative usage snapshot may be observed exactly at end after traffic
// has stopped. Later snapshots are always rejected.
func validUsageObservation(startedAt, periodEndsAt, observedAt time.Time) bool {
	return !observedAt.Before(startedAt) && !observedAt.After(periodEndsAt)
}
