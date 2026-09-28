package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

// SetGroupEnabled changes the operational state of a resource group. The
// global outbox event makes every enrolled Agent recompile its snapshot so a
// disabled group cannot keep serving already-authorized resources.
func (r *PostgresRepository) SetGroupEnabled(ctx context.Context, groupID string, enabled bool, actorID, requestID string) (ResourceGroup, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ResourceGroup{}, fmt.Errorf("begin resource group status change: %w", err)
	}
	defer tx.Rollback(ctx)
	var before ResourceGroup
	err = tx.QueryRow(ctx, `SELECT id::text,code,name,region,enabled,created_at FROM resource_groups WHERE id=$1 FOR UPDATE`, groupID).
		Scan(&before.ID, &before.Code, &before.Name, &before.Region, &before.Enabled, &before.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResourceGroup{}, ErrNotFound
	}
	if err != nil {
		return ResourceGroup{}, fmt.Errorf("lock resource group: %w", err)
	}
	if before.Enabled == enabled {
		if err := tx.Commit(ctx); err != nil {
			return ResourceGroup{}, fmt.Errorf("commit unchanged resource group: %w", err)
		}
		return before, nil
	}
	var after ResourceGroup
	err = tx.QueryRow(ctx, `UPDATE resource_groups SET enabled=$2 WHERE id=$1
RETURNING id::text,code,name,region,enabled,created_at`, groupID, enabled).
		Scan(&after.ID, &after.Code, &after.Name, &after.Region, &after.Enabled, &after.CreatedAt)
	if err != nil {
		return ResourceGroup{}, fmt.Errorf("update resource group status: %w", err)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return ResourceGroup{}, err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return ResourceGroup{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return ResourceGroup{}, err
	}
	eventID, err := id.NewV7()
	if err != nil {
		return ResourceGroup{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id)
VALUES ($1,$2,'update','resource_group',$3,$4,$5,$6)`, auditID, actorID, groupID, string(beforeJSON), string(afterJSON), requestID); err != nil {
		return ResourceGroup{}, fmt.Errorf("audit resource group status: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
VALUES ($1,'group.changed',$2::uuid,jsonb_build_object('group_id',($2::uuid)::text),$3)`, eventID, groupID, "group-status:"+eventID); err != nil {
		return ResourceGroup{}, fmt.Errorf("queue resource group convergence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ResourceGroup{}, fmt.Errorf("commit resource group status: %w", err)
	}
	return after, nil
}
