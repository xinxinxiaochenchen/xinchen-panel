package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"controlplane/internal/entitlement"
)

// Overview is the current frozen billing period shown to its owner. Available
// bytes exclude active lease reservations, which may contain unreported usage.
type Overview struct {
	PeriodID        string               `json:"period_id"`
	MembershipID    string               `json:"membership_id"`
	MembershipEnds  time.Time            `json:"membership_ends_at"`
	StartsAt        time.Time            `json:"starts_at"`
	EndsAt          time.Time            `json:"ends_at"`
	QuotaBytes      int64                `json:"quota_bytes"`
	UploadedBytes   int64                `json:"uploaded_bytes"`
	DownloadedBytes int64                `json:"downloaded_bytes"`
	ChargedBytes    int64                `json:"charged_bytes"`
	ReservedBytes   int64                `json:"reserved_bytes"`
	RemainingBytes  int64                `json:"remaining_bytes"`
	AvailableBytes  int64                `json:"available_bytes"`
	UsagePercent    float64              `json:"usage_percent"`
	Snapshot        entitlement.Snapshot `json:"snapshot"`
}

func (r *PostgresRepository) GetCurrentOverview(ctx context.Context, userID string) (Overview, error) {
	if !uuidPattern.MatchString(userID) {
		return Overview{}, ErrNotFound
	}
	var overview Overview
	var snapshot []byte
	err := r.pool.QueryRow(ctx, `SELECT p.id::text,p.membership_id::text,m.ends_at,p.starts_at,p.ends_at,
 p.quota_bytes,p.uploaded_bytes,p.downloaded_bytes,p.charged_bytes,p.reserved_bytes,p.snapshot_json
 FROM billing_periods p JOIN memberships m ON m.id=p.membership_id
 WHERE p.user_id=$1 AND m.status='active' AND p.status='open'
 AND p.starts_at<=statement_timestamp() AND p.ends_at>statement_timestamp()
 ORDER BY p.starts_at DESC LIMIT 1`, userID).Scan(&overview.PeriodID, &overview.MembershipID,
		&overview.MembershipEnds, &overview.StartsAt, &overview.EndsAt, &overview.QuotaBytes,
		&overview.UploadedBytes, &overview.DownloadedBytes, &overview.ChargedBytes,
		&overview.ReservedBytes, &snapshot)
	if err != nil {
		return Overview{}, databaseError(err)
	}
	if err := json.Unmarshal(snapshot, &overview.Snapshot); err != nil {
		return Overview{}, fmt.Errorf("decode billing period snapshot: %w", err)
	}
	if overview.ChargedBytes < overview.QuotaBytes {
		overview.RemainingBytes = overview.QuotaBytes - overview.ChargedBytes
	}
	if overview.ReservedBytes < overview.RemainingBytes {
		overview.AvailableBytes = overview.RemainingBytes - overview.ReservedBytes
	}
	if overview.QuotaBytes > 0 {
		overview.UsagePercent = min(100, 100*float64(overview.ChargedBytes)/float64(overview.QuotaBytes))
	}
	return overview, nil
}
