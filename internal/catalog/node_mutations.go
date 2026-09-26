package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

// SetNodeEnabled changes only the operational state. A durable outbox event
// asks the convergence worker to publish the resulting node configuration.
func (r *PostgresRepository) SetNodeEnabled(ctx context.Context, nodeID string, enabled bool, actorID, requestID string) (Node, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Node{}, fmt.Errorf("begin node status change: %w", err)
	}
	defer tx.Rollback(ctx)
	var current bool
	err = tx.QueryRow(ctx, `SELECT enabled FROM nodes WHERE id=$1 FOR UPDATE`, nodeID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("lock node: %w", err)
	}
	before, err := scanNode(tx.QueryRow(ctx, nodeSelect+` WHERE n.id=$1`, nodeID))
	if err != nil {
		return Node{}, fmt.Errorf("read node before update: %w", err)
	}
	if current == enabled {
		return before, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET enabled=$2,updated_at=clock_timestamp() WHERE id=$1`, nodeID, enabled); err != nil {
		return Node{}, fmt.Errorf("update node status: %w", err)
	}
	after, err := scanNode(tx.QueryRow(ctx, nodeSelect+` WHERE n.id=$1`, nodeID))
	if err != nil {
		return Node{}, fmt.Errorf("read node after update: %w", err)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return Node{}, err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return Node{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return Node{}, err
	}
	eventID, err := id.NewV7()
	if err != nil {
		return Node{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id)
VALUES ($1,$2,'update','node',$3,$4,$5,$6)`, auditID, actorID, nodeID, string(beforeJSON), string(afterJSON), requestID); err != nil {
		return Node{}, fmt.Errorf("audit node status: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
VALUES ($1,'node.changed',$2::uuid,jsonb_build_object('node_id',($2::uuid)::text),$3)`, eventID, nodeID, "node-status:"+eventID); err != nil {
		return Node{}, fmt.Errorf("queue node status convergence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Node{}, fmt.Errorf("commit node status: %w", err)
	}
	return after, nil
}
