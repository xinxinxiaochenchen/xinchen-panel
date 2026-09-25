package identity

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type ExpiredSessionRepository interface {
	DeleteExpiredSessions(context.Context, time.Time, int) (int64, error)
}

func PruneExpiredSessions(ctx context.Context, repository ExpiredSessionRepository, before time.Time) (int64, error) {
	var total int64
	for batch := 0; batch < 10; batch++ {
		deleted, err := repository.DeleteExpiredSessions(ctx, before, 500)
		if err != nil {
			return total, fmt.Errorf("prune expired sessions: %w", err)
		}
		total += deleted
		if deleted < 500 {
			break
		}
	}
	return total, nil
}

func RunSessionJanitor(ctx context.Context, repository ExpiredSessionRepository, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		deleted, err := PruneExpiredSessions(ctx, repository, time.Now())
		if err != nil && ctx.Err() == nil {
			logger.Warn("session cleanup failed", "error", err)
		} else if deleted > 0 {
			logger.Info("expired sessions removed", "count", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
