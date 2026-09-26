package billing

import (
	"context"
	"time"
)

type DailyUsage struct {
	Date            string `json:"date"`
	UploadedBytes   int64  `json:"uploaded_bytes"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	ChargedBytes    int64  `json:"charged_bytes"`
}

// ListDailyUsage groups immutable usage events by UTC calendar day. until is
// exclusive; the HTTP API converts an inclusive date parameter to this bound.
func (r *PostgresRepository) ListDailyUsage(ctx context.Context, userID string, from, until time.Time) ([]DailyUsage, error) {
	if !uuidPattern.MatchString(userID) || from.IsZero() || !from.Before(until) || until.Sub(from) > 91*24*time.Hour {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT ((observed_at AT TIME ZONE 'UTC')::date)::text AS usage_date,
 COALESCE(sum(uploaded_bytes),0)::bigint AS uploaded_bytes,
 COALESCE(sum(downloaded_bytes),0)::bigint AS downloaded_bytes,
 COALESCE(sum(charged_bytes),0)::bigint AS charged_bytes
 FROM usage_events WHERE user_id=$1 AND observed_at >= $2 AND observed_at < $3
 GROUP BY (observed_at AT TIME ZONE 'UTC')::date ORDER BY (observed_at AT TIME ZONE 'UTC')::date`, userID, from, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]DailyUsage, 0)
	for rows.Next() {
		var item DailyUsage
		if err := rows.Scan(&item.Date, &item.UploadedBytes, &item.DownloadedBytes, &item.ChargedBytes); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
