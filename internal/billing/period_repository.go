package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/entitlement"
	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

// EnsurePeriod creates immutable accounting history. Only PeriodWorker activates
// the current period's authorizations; querying historical periods never does.
func (r *PostgresRepository) EnsurePeriod(ctx context.Context, membershipID string, at time.Time) (Period, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Period{}, err
	}
	defer tx.Rollback(ctx)
	p, err := ensurePeriod(ctx, tx, membershipID, at)
	if err != nil {
		return Period{}, err
	}
	return p, tx.Commit(ctx)
}

func ensurePeriod(ctx context.Context, tx pgx.Tx, membershipID string, at time.Time) (Period, error) {
	if !uuidPattern.MatchString(membershipID) {
		return Period{}, ErrNotFound
	}
	var schedule Schedule
	var userID, planID string
	var snapshotJSON []byte
	var locked string
	err := tx.QueryRow(ctx, `SELECT user_id::text FROM memberships WHERE id=$1`, membershipID).Scan(&userID)
	if err != nil {
		return Period{}, databaseError(err)
	}
	err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE id=$1 AND status='active' FOR SHARE`, userID).Scan(&locked)
	if err != nil {
		return Period{}, databaseError(err)
	}
	err = tx.QueryRow(ctx, `SELECT m.user_id::text,m.plan_id::text,m.starts_at,m.ends_at,m.anchor_day,m.timezone,m.period_months,COALESCE(m.initial_snapshot_json,m.snapshot_json)
 FROM memberships m WHERE m.id=$1 AND m.status='active' FOR UPDATE`, membershipID).Scan(&userID, &planID, &schedule.StartsAt, &schedule.EndsAt, &schedule.AnchorDay, &schedule.Timezone, &schedule.PeriodMonths, &snapshotJSON)
	if err != nil {
		return Period{}, fmt.Errorf("load billing membership: %w", databaseError(err))
	}
	if at.IsZero() {
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
			return Period{}, err
		}
	}
	window, err := schedule.PeriodAt(at)
	if err != nil {
		return Period{}, err
	}
	existing, err := scanPeriod(tx.QueryRow(ctx, `SELECT `+periodColumns+` FROM billing_periods WHERE membership_id=$1 AND starts_at=$2`, membershipID, window.StartsAt))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Period{}, err
	}
	if !window.StartsAt.Equal(schedule.StartsAt) {
		// A single statement reads a consistent plan, limits and grants snapshot.
		err = tx.QueryRow(ctx, `SELECT jsonb_build_object(
   'plan_name',p.name,'quota_bytes',p.quota_bytes,'default_multiplier_milli',p.default_multiplier_milli,
   'limits',to_jsonb(l)-'plan_id',
   'resource_group_ids',COALESCE((SELECT jsonb_agg(g.resource_group_id ORDER BY g.resource_group_id) FROM plan_resource_group_grants g WHERE g.plan_id=p.id AND g.allowed),'[]'::jsonb),
   'line_ids',COALESCE((SELECT jsonb_agg(g.line_id ORDER BY g.line_id) FROM plan_line_grants g WHERE g.plan_id=p.id AND g.allowed),'[]'::jsonb))
   FROM plans p JOIN plan_limits l ON l.plan_id=p.id WHERE p.id=$1 FOR SHARE OF p,l`, planID).Scan(&snapshotJSON)
		if err != nil {
			return Period{}, fmt.Errorf("snapshot renewal plan: %w", databaseError(err))
		}
	}
	var snapshot entitlement.Snapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		return Period{}, fmt.Errorf("decode billing snapshot: %w", err)
	}
	if snapshot.QuotaBytes < 0 || snapshot.DefaultMultiplierMilli < 1 || snapshot.DefaultMultiplierMilli > 100000 {
		return Period{}, ErrInvalidMeter
	}
	periodID, err := id.NewV7()
	if err != nil {
		return Period{}, err
	}
	period, err := scanPeriod(tx.QueryRow(ctx, `INSERT INTO billing_periods(id,membership_id,user_id,starts_at,ends_at,quota_bytes,snapshot_json)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+periodColumns, periodID, membershipID, userID, window.StartsAt, window.EndsAt, snapshot.QuotaBytes, snapshotJSON))
	if err != nil {
		return Period{}, fmt.Errorf("create billing period: %w", err)
	}
	return period, nil
}
