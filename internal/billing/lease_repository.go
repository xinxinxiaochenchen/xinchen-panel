package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

const MaxLeaseBytes int64 = 1 << 20
const LeaseLifetime = 30 * time.Second
const leaseColumns = `id::text,agent_id::text,billing_period_id::text,request_id::text,requested_bytes,granted_bytes,consumed_bytes,issued_at,expires_at,state`

func scanLease(row pgx.Row) (Lease, error) {
	var l Lease
	err := row.Scan(&l.ID, &l.AgentID, &l.PeriodID, &l.RequestID, &l.RequestedBytes, &l.GrantedBytes, &l.ConsumedBytes, &l.IssuedAt, &l.ExpiresAt, &l.State)
	return l, databaseError(err)
}

func lockPeriod(ctx context.Context, tx pgx.Tx, periodID string) error {
	var locked string
	return databaseError(tx.QueryRow(ctx, `SELECT id::text FROM billing_periods WHERE id=$1 FOR UPDATE`, periodID).Scan(&locked))
}

func checkLeaseRequest(l Lease, req LeaseRequest) (Lease, error) {
	if !sameID(l.PeriodID, req.PeriodID) || l.RequestedBytes != req.RequestedBytes {
		return Lease{}, ErrConflict
	}
	return l, nil
}

// GrantLease returns the original grant on a retry, including its expiry and
// settled state. A retry never extends a grant or reserves additional quota.
func (r *PostgresRepository) GrantLease(ctx context.Context, req LeaseRequest) (Lease, error) {
	if !uuidPattern.MatchString(req.AgentID) || !uuidPattern.MatchString(req.PeriodID) || !uuidPattern.MatchString(req.RequestID) {
		return Lease{}, ErrNotFound
	}
	if req.RequestedBytes < 1 || req.RequestedBytes > MaxLeaseBytes {
		return Lease{}, ErrInvalidMeter
	}
	const existingSQL = `SELECT ` + leaseColumns + ` FROM quota_leases WHERE agent_id=$1 AND request_id=$2`
	if old, err := scanLease(r.pool.QueryRow(ctx, existingSQL, req.AgentID, req.RequestID)); err == nil {
		return checkLeaseRequest(old, req)
	} else if !errors.Is(err, ErrNotFound) {
		return Lease{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Lease{}, err
	}
	defer tx.Rollback(ctx)
	var userID, membershipID, locked string
	err = tx.QueryRow(ctx, `SELECT user_id::text,membership_id::text FROM billing_periods WHERE id=$1`, req.PeriodID).Scan(&userID, &membershipID)
	if err != nil {
		return Lease{}, databaseError(err)
	}
	// Match the account -> membership lock order used by membership creation.
	err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE id=$1 AND status='active' FOR SHARE`, userID).Scan(&locked)
	if err != nil {
		return Lease{}, databaseError(err)
	}
	var membershipStart, membershipEnd time.Time
	err = tx.QueryRow(ctx, `SELECT starts_at,ends_at FROM memberships WHERE id=$1 AND status='active' FOR SHARE`, membershipID).Scan(&membershipStart, &membershipEnd)
	if err != nil {
		return Lease{}, databaseError(err)
	}
	if err := lockPeriod(ctx, tx, req.PeriodID); err != nil {
		return Lease{}, err
	}
	if old, err := scanLease(tx.QueryRow(ctx, existingSQL, req.AgentID, req.RequestID)); err == nil {
		matched, e := checkLeaseRequest(old, req)
		if e != nil {
			return Lease{}, e
		}
		return matched, tx.Commit(ctx)
	} else if !errors.Is(err, ErrNotFound) {
		return Lease{}, err
	}
	var quota, charged, reserved int64
	var starts, ends, lastSeen, now time.Time
	var activatedAt *time.Time
	var status string
	err = tx.QueryRow(ctx, `SELECT p.quota_bytes,p.charged_bytes,p.reserved_bytes,p.starts_at,p.ends_at,p.status,a.last_seen_at,p.activated_at
 FROM billing_periods p JOIN agents a ON a.id=$2 JOIN nodes n ON n.id=a.node_id JOIN resource_groups g ON g.id=n.group_id
 WHERE p.id=$1 AND a.status='online' AND a.last_seen_at IS NOT NULL AND n.enabled AND g.enabled
 AND COALESCE(p.snapshot_json->'resource_group_ids','[]'::jsonb) ? n.group_id::text
 FOR SHARE OF a,n,g`, req.PeriodID, req.AgentID).Scan(&quota, &charged, &reserved, &starts, &ends, &status, &lastSeen, &activatedAt)
	if err != nil {
		return Lease{}, databaseError(err)
	}
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return Lease{}, err
	}
	if status != "open" || activatedAt == nil || now.Before(starts) || !now.Before(ends) || now.Before(membershipStart) || !now.Before(membershipEnd) || now.Sub(lastSeen) > 45*time.Second {
		return Lease{}, ErrNotFound
	}
	if charged >= quota || reserved >= quota-charged {
		return Lease{}, ErrQuotaExhausted
	}
	amount := min(req.RequestedBytes, quota-charged-reserved)
	expires := earlier(now.Add(LeaseLifetime), earlier(ends, membershipEnd))
	leaseID, err := id.NewV7()
	if err != nil {
		return Lease{}, err
	}
	lease, err := scanLease(tx.QueryRow(ctx, `INSERT INTO quota_leases(id,agent_id,billing_period_id,request_id,requested_bytes,granted_bytes,issued_at,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+leaseColumns, leaseID, req.AgentID, req.PeriodID, req.RequestID, req.RequestedBytes, amount, now, expires))
	if err != nil {
		return Lease{}, fmt.Errorf("insert quota lease: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_periods SET reserved_bytes=reserved_bytes+$2 WHERE id=$1`, req.PeriodID, amount); err != nil {
		return Lease{}, databaseError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Lease{}, databaseError(err)
	}
	return lease, nil
}

// SettleLease requires the Agent to stop consuming the lease and ACK all usage
// batches first. A mismatch leaves the reservation intact for reconciliation.
func (r *PostgresRepository) SettleLease(ctx context.Context, agentID, leaseID string, expectedConsumed int64) (Lease, error) {
	if !uuidPattern.MatchString(agentID) || !uuidPattern.MatchString(leaseID) {
		return Lease{}, ErrNotFound
	}
	if expectedConsumed < 0 {
		return Lease{}, ErrInvalidMeter
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Lease{}, err
	}
	defer tx.Rollback(ctx)
	var periodID string
	err = tx.QueryRow(ctx, `SELECT billing_period_id::text FROM quota_leases WHERE id=$1 AND agent_id=$2`, leaseID, agentID).Scan(&periodID)
	if err != nil {
		return Lease{}, databaseError(err)
	}
	if err := lockPeriod(ctx, tx, periodID); err != nil {
		return Lease{}, err
	}
	l, err := scanLease(tx.QueryRow(ctx, `SELECT `+leaseColumns+` FROM quota_leases WHERE id=$1 AND agent_id=$2 FOR UPDATE`, leaseID, agentID))
	if err != nil {
		return Lease{}, err
	}
	if l.ConsumedBytes != expectedConsumed {
		return Lease{}, ErrConflict
	}
	if l.State == "settled" {
		return l, tx.Commit(ctx)
	}
	release := max(l.GrantedBytes-l.ConsumedBytes, 0)
	if _, err := tx.Exec(ctx, `UPDATE billing_periods SET reserved_bytes=reserved_bytes-$2 WHERE id=$1`, periodID, release); err != nil {
		return Lease{}, databaseError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE quota_leases SET state='settled',settled_at=clock_timestamp() WHERE id=$1`, leaseID); err != nil {
		return Lease{}, databaseError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Lease{}, databaseError(err)
	}
	l.State = "settled"
	return l, nil
}

// consumeLease runs under the period and session locks held by RecordUsage.
// Actual traffic must be retained even when a data-plane boundary overruns its
// reservation. The Agent enforces the bound; the ledger does not discard bytes.
func consumeLease(ctx context.Context, tx pgx.Tx, agentID, periodID, leaseID string, delta int64, observedAt time.Time) (int64, error) {
	l, err := scanLease(tx.QueryRow(ctx, `SELECT `+leaseColumns+` FROM quota_leases WHERE id=$1 AND agent_id=$2 AND billing_period_id=$3 FOR UPDATE`, leaseID, agentID, periodID))
	if err != nil {
		return 0, err
	}
	if l.State != "active" {
		return 0, ErrLeaseClosed
	}
	if observedAt.Before(l.IssuedAt) || observedAt.After(l.ExpiresAt) {
		return 0, ErrInvalidMeter
	}
	used := min(delta, max(l.GrantedBytes-l.ConsumedBytes, 0))
	if _, err := tx.Exec(ctx, `UPDATE quota_leases SET consumed_bytes=consumed_bytes+$2 WHERE id=$1`, l.ID, delta); err != nil {
		return 0, databaseError(err)
	}
	return used, nil
}
