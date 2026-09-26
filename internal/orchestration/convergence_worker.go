package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type configReconciler interface {
	Reconcile(context.Context, string) (DesiredRevision, bool, error)
}

type ConvergenceWorker struct {
	pool      *pgxpool.Pool
	revisions configReconciler
}

func NewConvergenceWorker(pool *pgxpool.Pool, revisions configReconciler) *ConvergenceWorker {
	return &ConvergenceWorker{pool: pool, revisions: revisions}
}

var eventNodeIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ProcessOne claims one relevant event. Reconciliation is idempotent, so an
// interrupted attempt can safely be repeated after its event lock is released.
func (w *ConvergenceWorker) ProcessOne(ctx context.Context) (bool, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin convergence event: %w", err)
	}
	defer tx.Rollback(ctx)
	var eventID, kind string
	var payload []byte
	var retries int
	err = tx.QueryRow(ctx, `SELECT id::text,kind,payload,retry_count FROM outbox_events
WHERE processed_at IS NULL AND available_at <= now()
AND kind IN ('forward_rule.changed','forward_policy.changed','proxy_access.changed')
ORDER BY available_at,created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&eventID, &kind, &payload, &retries)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim convergence event: %w", err)
	}
	err = w.reconcileEvent(ctx, tx, kind, payload)
	if err != nil {
		// Keep the lock while scheduling a retry; another worker cannot claim
		// this event between the failed reconcile and the delay update.
		if _, updateErr := tx.Exec(ctx, `UPDATE outbox_events SET retry_count=retry_count+1,
available_at=now()+($2::int * interval '1 second') WHERE id=$1`, eventID, retryDelaySeconds(retries+1)); updateErr != nil {
			return false, fmt.Errorf("schedule convergence retry after %v: %w", err, updateErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return false, fmt.Errorf("commit convergence retry after %v: %w", err, commitErr)
		}
		return false, fmt.Errorf("reconcile %s event %s: %w", kind, eventID, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE outbox_events SET processed_at=now() WHERE id=$1`, eventID); err != nil {
		return false, fmt.Errorf("complete convergence event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit convergence event: %w", err)
	}
	return true, nil
}

func retryDelaySeconds(attempt int) int {
	if attempt < 1 {
		return 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return 1 << attempt
}

func (w *ConvergenceWorker) reconcileEvent(ctx context.Context, tx pgx.Tx, kind string, payload []byte) error {
	if kind == "forward_rule.changed" || kind == "proxy_access.changed" {
		var event struct {
			NodeID string `json:"node_id"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			return fmt.Errorf("decode rule event: %w", err)
		}
		if !eventNodeIDPattern.MatchString(event.NodeID) {
			return errors.New("rule event has invalid node_id")
		}
		_, _, err := w.revisions.Reconcile(ctx, event.NodeID)
		if errors.Is(err, ErrAgentNotFound) {
			// Nodes without enrolled Agents have no configuration to deliver.
			return nil
		}
		return err
	}
	nodeIDs, err := agentNodeIDs(ctx, tx)
	if err != nil {
		return err
	}
	for _, nodeID := range nodeIDs {
		if _, _, err := w.revisions.Reconcile(ctx, nodeID); err != nil && !errors.Is(err, ErrAgentNotFound) {
			return fmt.Errorf("reconcile Agent %s: %w", nodeID, err)
		}
	}
	return nil
}

func agentNodeIDs(ctx context.Context, querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) ([]string, error) {
	rows, err := querier.Query(ctx, `SELECT node_id::text FROM agents ORDER BY node_id`)
	if err != nil {
		return nil, fmt.Errorf("list Agent nodes: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read Agent node: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Agent nodes: %w", err)
	}
	return ids, nil
}

// Sweep covers time-based authorization changes that do not write outbox rows.
func (w *ConvergenceWorker) Sweep(ctx context.Context) (int, error) {
	nodeIDs, err := agentNodeIDs(ctx, w.pool)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, nodeID := range nodeIDs {
		_, changed, err := w.revisions.Reconcile(ctx, nodeID)
		if errors.Is(err, ErrAgentNotFound) {
			continue
		}
		if err != nil {
			return created, fmt.Errorf("sweep Agent %s: %w", nodeID, err)
		}
		if changed {
			created++
		}
	}
	return created, nil
}

func (w *ConvergenceWorker) Run(ctx context.Context, logger *slog.Logger) {
	events := time.NewTicker(15 * time.Second)
	sweep := time.NewTicker(time.Minute)
	defer events.Stop()
	defer sweep.Stop()
	consume := func() {
		for ctx.Err() == nil {
			processed, err := w.ProcessOne(ctx)
			if err != nil {
				logger.Error("configuration convergence event failed", "error", err)
				return
			}
			if !processed {
				return
			}
		}
	}
	if count, err := w.Sweep(ctx); err != nil {
		logger.Error("configuration convergence sweep failed", "error", err)
	} else if count > 0 {
		logger.Info("configuration revisions staged", "count", count)
	}
	consume()
	for {
		select {
		case <-ctx.Done():
			return
		case <-events.C:
			consume()
		case <-sweep.C:
			if count, err := w.Sweep(ctx); err != nil {
				logger.Error("configuration convergence sweep failed", "error", err)
			} else if count > 0 {
				logger.Info("configuration revisions staged", "count", count)
			}
		}
	}
}
