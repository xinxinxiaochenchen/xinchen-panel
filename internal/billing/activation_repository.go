package billing

import (
	"context"
	"time"

	"controlplane/internal/platform/id"
)

func (r *PostgresRepository) activateCurrentPeriod(ctx context.Context, membershipID string) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	p, err := ensurePeriod(ctx, tx, membershipID, time.Time{})
	if err != nil {
		return false, err
	}
	if err := lockPeriod(ctx, tx, p.ID); err != nil {
		return false, err
	}
	var activated *time.Time
	var snapshot []byte
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT activated_at,snapshot_json,clock_timestamp() FROM billing_periods WHERE id=$1`, p.ID).Scan(&activated, &snapshot, &now)
	if err != nil {
		return false, databaseError(err)
	}
	if activated != nil {
		return false, tx.Commit(ctx)
	}
	if now.Before(p.StartsAt) || !now.Before(p.EndsAt) {
		return false, ErrOutsideMembership
	}
	if _, err := tx.Exec(ctx, `UPDATE memberships SET initial_snapshot_json=COALESCE(initial_snapshot_json,snapshot_json),snapshot_json=$2 WHERE id=$1`, membershipID, snapshot); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_periods SET status='closed' WHERE membership_id=$1 AND ends_at<=$2`, membershipID, p.StartsAt); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_periods SET activated_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		return false, err
	}
	eventID, err := id.NewV7()
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
 VALUES($1,'billing.period_renewed',$2,jsonb_build_object('user_id',$3::text,'period_id',$4::text),$5)`, eventID, membershipID, p.UserID, p.ID, "billing.period:"+p.ID); err != nil {
		return false, databaseError(err)
	}
	return true, tx.Commit(ctx)
}
