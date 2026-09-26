package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type PeriodWorker struct{ repository *PostgresRepository }

func NewPeriodWorker(repo *PostgresRepository) *PeriodWorker { return &PeriodWorker{repository: repo} }

// Sweep pages eligible members. Each activation reads a fresh database time
// after account/membership locks, so a slow sweep cannot activate a stale period.
func (w *PeriodWorker) Sweep(ctx context.Context) (int, error) {
	activated := 0
	after := ""
	var failures []error
	for {
		rows, err := w.repository.pool.Query(ctx, `SELECT m.id::text FROM memberships m JOIN users u ON u.id=m.user_id
  WHERE m.status='active' AND u.status='active' AND m.starts_at<=clock_timestamp() AND m.ends_at>clock_timestamp()
  AND m.id::text>$1 ORDER BY m.id::text LIMIT 100`, after)
		if err != nil {
			return activated, fmt.Errorf("list billing memberships: %w", err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return activated, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return activated, err
		}
		rows.Close()
		for _, id := range ids {
			changed, err := w.repository.activateCurrentPeriod(ctx, id)
			if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrOutsideMembership) {
				failures = append(failures, fmt.Errorf("activate billing period %s: %w", id, err))
			}
			if err == nil && changed {
				activated++
			}
		}
		if len(ids) < 100 {
			return activated, errors.Join(failures...)
		}
		after = ids[len(ids)-1]
	}
}

func (w *PeriodWorker) Run(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	sweep := func() {
		if count, err := w.Sweep(ctx); err != nil {
			if ctx.Err() == nil {
				logger.Error("billing period sweep failed", "error", err)
			}
		} else if count > 0 {
			logger.Info("billing periods activated", "count", count)
		}
	}
	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}
