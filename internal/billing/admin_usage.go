package billing

import (
	"context"
	"time"
)

type AdminUsageQuery struct {
	From      time.Time
	Until     time.Time
	GroupBy   string
	UserID    string
	NodeID    string
	LineID    string
	AfterDate string
	AfterID   string
	Limit     int
}

type UsageBucket struct {
	Date            string `json:"date"`
	DimensionID     string `json:"dimension_id"`
	UploadedBytes   int64  `json:"uploaded_bytes"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	ChargedBytes    int64  `json:"charged_bytes"`
}

func (r *PostgresRepository) ListAdminUsage(ctx context.Context, query AdminUsageQuery) ([]UsageBucket, error) {
	if query.From.IsZero() || !query.From.Before(query.Until) || query.Until.Sub(query.From) > 90*24*time.Hour ||
		query.Limit < 1 || query.Limit > 201 || query.GroupBy != "date" && query.GroupBy != "user" &&
		query.GroupBy != "node" && query.GroupBy != "line" {
		return nil, ErrNotFound
	}
	for _, value := range []string{query.UserID, query.NodeID, query.LineID, query.AfterID} {
		if value != "" && !uuidPattern.MatchString(value) {
			return nil, ErrNotFound
		}
	}
	var user, node, line any
	if query.UserID != "" {
		user = query.UserID
	}
	if query.NodeID != "" {
		node = query.NodeID
	}
	if query.LineID != "" {
		line = query.LineID
	}
	rows, err := r.pool.Query(ctx, `WITH grouped AS (
 SELECT ((observed_at AT TIME ZONE 'UTC')::date)::text AS usage_date,
 CASE $6::text WHEN 'user' THEN user_id::text WHEN 'node' THEN ingress_node_id::text
 WHEN 'line' THEN COALESCE(line_id::text,'') ELSE '' END AS dimension_id,
 sum(uploaded_bytes)::bigint AS uploaded_bytes,sum(downloaded_bytes)::bigint AS downloaded_bytes,
 sum(charged_bytes)::bigint AS charged_bytes
 FROM usage_events
 WHERE observed_at >= $1 AND observed_at < $2
 AND ($3::uuid IS NULL OR user_id=$3) AND ($4::uuid IS NULL OR ingress_node_id=$4)
 AND ($5::uuid IS NULL OR line_id=$5)
 GROUP BY 1,2
 ) SELECT usage_date,dimension_id,uploaded_bytes,downloaded_bytes,charged_bytes
 FROM grouped WHERE ($7::text='' OR (usage_date,dimension_id)>($7,$8))
 ORDER BY usage_date,dimension_id LIMIT $9`, query.From, query.Until, user, node, line,
		query.GroupBy, query.AfterDate, query.AfterID, query.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]UsageBucket, 0)
	for rows.Next() {
		var bucket UsageBucket
		if err := rows.Scan(&bucket.Date, &bucket.DimensionID, &bucket.UploadedBytes, &bucket.DownloadedBytes, &bucket.ChargedBytes); err != nil {
			return nil, err
		}
		result = append(result, bucket)
	}
	return result, rows.Err()
}
