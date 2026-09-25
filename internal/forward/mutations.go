package forward

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

func (r *PostgresRepository) UpdateOwnRule(ctx context.Context, ownerID, ruleID string, patch RulePatch, requestID string) (Rule, error) {
	patch, err := NormalizeRulePatch(patch)
	if err != nil {
		return Rule{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Rule{}, fmt.Errorf("begin forward rule update: %w", err)
	}
	defer tx.Rollback(ctx)
	var before Rule
	var membershipID string
	if patch.Enabled != nil && *patch.Enabled {
		preview, err := scanRule(tx.QueryRow(ctx, ruleSelect+` WHERE id=$1 AND user_id=$2`, ruleID, ownerID))
		if errors.Is(err, pgx.ErrNoRows) {
			return Rule{}, ErrNotFound
		}
		if err != nil {
			return Rule{}, fmt.Errorf("read forward rule before re-enable: %w", err)
		}
		var userStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, ownerID).Scan(&userStatus); errors.Is(err, pgx.ErrNoRows) {
			return Rule{}, ErrNotFound
		} else if err != nil {
			return Rule{}, fmt.Errorf("lock forward rule owner: %w", err)
		}
		if userStatus != "active" {
			return Rule{}, ErrNotFound
		}
		var snapshot entitlementSnapshot
		membershipID, snapshot, err = currentEntitlement(ctx, tx, ownerID)
		if err != nil {
			return Rule{}, err
		}
		if snapshot.Limits.MaxForwardRulesPerNode <= 0 {
			return Rule{}, ErrNotFound
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM forward_rules WHERE user_id=$1 AND ingress_node_id=$2`, ownerID, preview.IngressNodeID).Scan(&count); err != nil {
			return Rule{}, fmt.Errorf("count forward rules: %w", err)
		}
		if count > snapshot.Limits.MaxForwardRulesPerNode {
			return Rule{}, ErrLimitReached
		}
		groupID, err := lockIngress(ctx, tx, preview.IngressNodeID, preview.IngressPort)
		if err != nil {
			return Rule{}, err
		}
		if !slices.Contains(snapshot.ResourceGroupIDs, groupID) {
			return Rule{}, ErrNotFound
		}
		policyKind := "public_host"
		var policyGroupID *string
		if preview.TargetNodeID != nil {
			targetGroup, err := lockTargetNode(ctx, tx, *preview.TargetNodeID)
			if err != nil {
				return Rule{}, err
			}
			if !slices.Contains(snapshot.ResourceGroupIDs, targetGroup) {
				return Rule{}, ErrNotFound
			}
			policyKind = "node"
			policyGroupID = &targetGroup
		}
		if err := requireTargetPolicies(ctx, tx, policyKind, policyGroupID, preview.Protocol, preview.TargetPort); err != nil {
			return Rule{}, err
		}
		before, err = lockedOwnRule(ctx, tx, ownerID, ruleID)
		if err != nil {
			return Rule{}, err
		}
		if before.IngressNodeID != preview.IngressNodeID || before.IngressPort != preview.IngressPort ||
			before.TargetPort != preview.TargetPort || before.Protocol != preview.Protocol ||
			!sameStringPtr(before.TargetNodeID, preview.TargetNodeID) || !sameStringPtr(before.TargetHost, preview.TargetHost) {
			return Rule{}, ErrConflict
		}
	} else {
		before, err = lockedOwnRule(ctx, tx, ownerID, ruleID)
		if err != nil {
			return Rule{}, err
		}
	}
	name := before.Name
	if patch.Name != nil {
		name = *patch.Name
	}
	enabled := before.Enabled
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	status := "disabled"
	if before.Enabled || enabled || before.ApplyStatus == "pending" {
		status = "pending"
	}
	if _, err := tx.Exec(ctx, `UPDATE forward_rules SET name=$2,enabled=$3,apply_status=$4,updated_at=now() WHERE id=$1`, ruleID, name, enabled, status); err != nil {
		return Rule{}, fmt.Errorf("update forward rule: %w", mapDatabaseError(err))
	}
	after, err := scanRule(tx.QueryRow(ctx, ruleSelect+` WHERE id=$1`, ruleID))
	if err != nil {
		return Rule{}, fmt.Errorf("reload forward rule: %w", err)
	}
	if err := recordRuleChange(ctx, tx, after, ownerID, "update", requestID, &before); err != nil {
		return Rule{}, err
	}
	if membershipID != "" {
		if err := recheckMembership(ctx, tx, membershipID); err != nil {
			return Rule{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Rule{}, fmt.Errorf("commit forward rule update: %w", mapDatabaseError(err))
	}
	return after, nil
}

func sameStringPtr(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (r *PostgresRepository) DeleteOwnRule(ctx context.Context, ownerID, ruleID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin forward rule deletion: %w", err)
	}
	defer tx.Rollback(ctx)
	before, err := lockedOwnRule(ctx, tx, ownerID, ruleID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE port_allocations SET released_at=now()
WHERE owner_type='forward_rule' AND owner_id=$1 AND released_at IS NULL`, ruleID); err != nil {
		return fmt.Errorf("release forward ports: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM forward_rules WHERE id=$1`, ruleID); err != nil {
		return fmt.Errorf("delete forward rule: %w", mapDatabaseError(err))
	}
	if err := recordRuleChange(ctx, tx, before, ownerID, "delete", requestID, &before); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit forward rule deletion: %w", mapDatabaseError(err))
	}
	return nil
}

func lockedOwnRule(ctx context.Context, tx pgx.Tx, ownerID, ruleID string) (Rule, error) {
	rule, err := scanRule(tx.QueryRow(ctx, ruleSelect+` WHERE id=$1 AND user_id=$2 FOR UPDATE`, ruleID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	if err != nil {
		return Rule{}, fmt.Errorf("lock owned forward rule: %w", err)
	}
	return rule, nil
}
