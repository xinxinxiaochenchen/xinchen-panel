package identity

import (
	"context"
	"testing"
	"time"
)

type pruneStub struct {
	calls  int
	before time.Time
}

func (s *pruneStub) DeleteExpiredSessions(_ context.Context, before time.Time, limit int) (int64, error) {
	s.calls++
	s.before = before
	if limit != 500 {
		return 0, ErrInvalidInput
	}
	if s.calls == 1 {
		return 500, nil
	}
	return 3, nil
}

func TestPruneExpiredSessionsDrainsBatches(t *testing.T) {
	repo := &pruneStub{}
	before := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	deleted, err := PruneExpiredSessions(context.Background(), repo, before)
	if err != nil || deleted != 503 || repo.calls != 2 || !repo.before.Equal(before) {
		t.Fatalf("prune = %d, %v, calls=%d", deleted, err, repo.calls)
	}
}
