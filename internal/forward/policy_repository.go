package forward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

const targetPolicySelect = `SELECT id::text,kind,target_group_id::text,protocol,port_start,port_end,enabled,created_at,updated_at FROM forward_target_policies`

func scanTargetPolicy(row pgx.Row) (TargetPolicy, error) {
	var policy TargetPolicy
	err := row.Scan(&policy.ID, &policy.Kind, &policy.TargetGroupID, &policy.Protocol,
		&policy.PortStart, &policy.PortEnd, &policy.Enabled, &policy.CreatedAt, &policy.UpdatedAt)
	return policy, err
}

func (r *PostgresRepository) CreateTargetPolicy(ctx context.Context, input TargetPolicyInput, actorID, requestID string) (TargetPolicy, error) {
	enabled := input.Enabled
	input, err := NormalizeTargetPolicy(NewTargetPolicy{Kind: input.Kind, TargetGroupID: input.TargetGroupID,
		Protocol: input.Protocol, PortStart: input.PortStart, PortEnd: input.PortEnd, Enabled: &enabled})
	if err != nil {
		return TargetPolicy{}, err
	}
	policyID, err := id.NewV7()
	if err != nil {
		return TargetPolicy{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return TargetPolicy{}, fmt.Errorf("begin target policy creation: %w", err)
	}
	defer tx.Rollback(ctx)
	if input.TargetGroupID != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM resource_groups WHERE id=$1)`, *input.TargetGroupID).Scan(&exists); err != nil {
			return TargetPolicy{}, fmt.Errorf("check target policy group: %w", err)
		}
		if !exists {
			return TargetPolicy{}, ErrNotFound
		}
	}
	policy := TargetPolicy{ID: policyID, Kind: input.Kind, TargetGroupID: input.TargetGroupID,
		Protocol: input.Protocol, PortStart: input.PortStart, PortEnd: input.PortEnd, Enabled: input.Enabled}
	err = tx.QueryRow(ctx, `INSERT INTO forward_target_policies(id,kind,target_group_id,protocol,port_start,port_end,enabled)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at,updated_at`, policy.ID, policy.Kind,
		policy.TargetGroupID, policy.Protocol, policy.PortStart, policy.PortEnd, policy.Enabled).Scan(&policy.CreatedAt, &policy.UpdatedAt)
	if err != nil {
		return TargetPolicy{}, fmt.Errorf("insert target policy: %w", mapDatabaseError(err))
	}
	if err := recordPolicyChange(ctx, tx, policy, actorID, "create", requestID, nil); err != nil {
		return TargetPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TargetPolicy{}, fmt.Errorf("commit target policy creation: %w", mapDatabaseError(err))
	}
	return policy, nil
}

func (r *PostgresRepository) ListTargetPolicies(ctx context.Context, limit int, afterID string) ([]TargetPolicy, error) {
	rows, err := r.pool.Query(ctx, targetPolicySelect+` WHERE id::text>$2 ORDER BY id::text LIMIT $1`, limit, afterID)
	if err != nil {
		return nil, fmt.Errorf("list target policies: %w", err)
	}
	defer rows.Close()
	policies := make([]TargetPolicy, 0)
	for rows.Next() {
		policy, err := scanTargetPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("scan target policy: %w", err)
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate target policies: %w", err)
	}
	return policies, nil
}

func (r *PostgresRepository) GetTargetPolicy(ctx context.Context, policyID string) (TargetPolicy, error) {
	policy, err := scanTargetPolicy(r.pool.QueryRow(ctx, targetPolicySelect+` WHERE id=$1`, policyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return TargetPolicy{}, ErrNotFound
	}
	if err != nil {
		return TargetPolicy{}, fmt.Errorf("get target policy: %w", err)
	}
	return policy, nil
}

func (r *PostgresRepository) UpdateTargetPolicy(ctx context.Context, policyID string, patch TargetPolicyPatch, actorID, requestID string) (TargetPolicy, error) {
	patch, err := NormalizeTargetPolicyPatch(patch)
	if err != nil {
		return TargetPolicy{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return TargetPolicy{}, fmt.Errorf("begin target policy update: %w", err)
	}
	defer tx.Rollback(ctx)
	before, err := scanTargetPolicy(tx.QueryRow(ctx, targetPolicySelect+` WHERE id=$1 FOR UPDATE`, policyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return TargetPolicy{}, ErrNotFound
	}
	if err != nil {
		return TargetPolicy{}, fmt.Errorf("lock target policy: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE forward_target_policies SET enabled=$2,updated_at=now() WHERE id=$1`, policyID, *patch.Enabled); err != nil {
		return TargetPolicy{}, fmt.Errorf("update target policy: %w", err)
	}
	if err := markPolicyRulesPending(ctx, tx, before); err != nil {
		return TargetPolicy{}, err
	}
	after, err := scanTargetPolicy(tx.QueryRow(ctx, targetPolicySelect+` WHERE id=$1`, policyID))
	if err != nil {
		return TargetPolicy{}, fmt.Errorf("reload target policy: %w", err)
	}
	if err := recordPolicyChange(ctx, tx, after, actorID, "update", requestID, &before); err != nil {
		return TargetPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TargetPolicy{}, fmt.Errorf("commit target policy update: %w", err)
	}
	return after, nil
}

func (r *PostgresRepository) DeleteTargetPolicy(ctx context.Context, policyID, actorID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin target policy deletion: %w", err)
	}
	defer tx.Rollback(ctx)
	before, err := scanTargetPolicy(tx.QueryRow(ctx, targetPolicySelect+` WHERE id=$1 FOR UPDATE`, policyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock target policy: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM forward_target_policies WHERE id=$1`, policyID); err != nil {
		return fmt.Errorf("delete target policy: %w", err)
	}
	if err := markPolicyRulesPending(ctx, tx, before); err != nil {
		return err
	}
	if err := recordPolicyChange(ctx, tx, before, actorID, "delete", requestID, &before); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit target policy deletion: %w", err)
	}
	return nil
}

func recordPolicyChange(ctx context.Context, tx pgx.Tx, policy TargetPolicy, actorID, action, requestID string, before *TargetPolicy) error {
	auditID, err := id.NewV7()
	if err != nil {
		return err
	}
	outboxID, err := id.NewV7()
	if err != nil {
		return err
	}
	var beforeJSON, afterJSON any
	if before != nil {
		encoded, err := json.Marshal(before)
		if err != nil {
			return err
		}
		beforeJSON = string(encoded)
	}
	if action != "delete" {
		encoded, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		afterJSON = string(encoded)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id)
VALUES ($1,$2,$3,'forward_target_policy',$4,$5,$6,$7)`, auditID, actorID, action, policy.ID, beforeJSON, afterJSON, requestID); err != nil {
		return fmt.Errorf("audit target policy: %w", err)
	}
	payload := map[string]string{"policy_id": policy.ID, "action": action}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
VALUES ($1,'forward_policy.changed',$2,$3,$4)`, outboxID, policy.ID, payload, "forward-policy:"+outboxID); err != nil {
		return fmt.Errorf("queue target policy change: %w", err)
	}
	return nil
}

func markPolicyRulesPending(ctx context.Context, tx pgx.Tx, policy TargetPolicy) error {
	if policy.Kind == "public_host" {
		_, err := tx.Exec(ctx, `UPDATE forward_rules SET apply_status='pending',updated_at=now()
WHERE enabled AND target_host IS NOT NULL AND target_port BETWEEN $1 AND $2
AND protocol IN ($3,'BOTH') AND apply_status<>'pending'`, policy.PortStart, policy.PortEnd, policy.Protocol)
		if err != nil {
			return fmt.Errorf("mark public-host rules pending: %w", err)
		}
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE forward_rules r SET apply_status='pending',updated_at=now()
FROM nodes target WHERE r.target_node_id=target.id AND r.enabled AND r.target_port BETWEEN $1 AND $2
AND r.protocol IN ($3,'BOTH') AND target.group_id=$4 AND r.apply_status<>'pending'`,
		policy.PortStart, policy.PortEnd, policy.Protocol, policy.TargetGroupID)
	if err != nil {
		return fmt.Errorf("mark node-target rules pending: %w", err)
	}
	return nil
}
