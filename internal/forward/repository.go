package forward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("forward rule not found")
	ErrConflict     = errors.New("forward rule conflict")
	ErrLimitReached = errors.New("forward rule limit reached")
	ErrPolicyDenied = errors.New("forward target policy denied")
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

type entitlementSnapshot struct {
	ResourceGroupIDs []string `json:"resource_group_ids"`
	Limits           struct {
		MaxForwardRulesPerNode int `json:"max_forward_rules_per_node"`
	} `json:"limits"`
}

func (r *PostgresRepository) CreateRule(ctx context.Context, input RuleInput, ownerID, requestID string) (Rule, error) {
	normalized, err := NormalizeRule(NewRule{Name: input.Name, IngressNodeID: input.IngressNodeID,
		IngressPort: input.IngressPort, TargetNodeID: input.TargetNodeID, TargetHost: input.TargetHost,
		TargetPort: input.TargetPort, Protocol: input.Protocol, Enabled: &input.Enabled})
	if err != nil {
		return Rule{}, err
	}
	ruleID, err := id.NewV7()
	if err != nil {
		return Rule{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Rule{}, fmt.Errorf("begin forward rule creation: %w", err)
	}
	defer tx.Rollback(ctx)
	var userStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, ownerID).Scan(&userStatus); errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, ErrNotFound
	} else if err != nil {
		return Rule{}, fmt.Errorf("lock forward rule owner: %w", err)
	}
	if userStatus != "active" {
		return Rule{}, ErrNotFound
	}
	membershipID, snapshot, err := currentEntitlement(ctx, tx, ownerID)
	if err != nil {
		return Rule{}, err
	}
	if snapshot.Limits.MaxForwardRulesPerNode <= 0 {
		return Rule{}, ErrNotFound
	}
	ingressGroup, err := lockIngress(ctx, tx, normalized.IngressNodeID, normalized.IngressPort)
	if err != nil {
		return Rule{}, err
	}
	if !slices.Contains(snapshot.ResourceGroupIDs, ingressGroup) {
		return Rule{}, ErrNotFound
	}
	policyKind := "public_host"
	var policyGroupID *string
	if normalized.TargetNodeID != nil {
		targetGroup, err := lockTargetNode(ctx, tx, *normalized.TargetNodeID)
		if err != nil {
			return Rule{}, err
		}
		if !slices.Contains(snapshot.ResourceGroupIDs, targetGroup) || (*normalized.TargetNodeID == normalized.IngressNodeID && normalized.TargetPort == normalized.IngressPort) {
			return Rule{}, ErrNotFound
		}
		policyKind = "node"
		policyGroupID = &targetGroup
	}
	if err := requireTargetPolicies(ctx, tx, policyKind, policyGroupID, normalized.Protocol, normalized.TargetPort); err != nil {
		return Rule{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM forward_rules WHERE user_id=$1 AND ingress_node_id=$2`, ownerID, normalized.IngressNodeID).Scan(&count); err != nil {
		return Rule{}, fmt.Errorf("count forward rules: %w", err)
	}
	if count >= snapshot.Limits.MaxForwardRulesPerNode {
		return Rule{}, ErrLimitReached
	}
	status := "pending"
	if !normalized.Enabled {
		status = "disabled"
	}
	rule := Rule{ID: ruleID, UserID: ownerID, Name: normalized.Name,
		IngressNodeID: normalized.IngressNodeID, IngressPort: normalized.IngressPort,
		TargetNodeID: normalized.TargetNodeID, TargetHost: normalized.TargetHost,
		TargetPort: normalized.TargetPort, Protocol: normalized.Protocol,
		Enabled: normalized.Enabled, ApplyStatus: status}
	err = tx.QueryRow(ctx, `INSERT INTO forward_rules(id,user_id,name,ingress_node_id,ingress_port,
target_node_id,target_host,target_port,protocol,enabled,apply_status)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING created_at,updated_at`,
		rule.ID, rule.UserID, rule.Name, rule.IngressNodeID, rule.IngressPort,
		rule.TargetNodeID, rule.TargetHost, rule.TargetPort, rule.Protocol,
		rule.Enabled, rule.ApplyStatus).Scan(&rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return Rule{}, fmt.Errorf("insert forward rule: %w", mapDatabaseError(err))
	}
	for _, protocol := range physicalProtocols(rule.Protocol) {
		allocationID, err := id.NewV7()
		if err != nil {
			return Rule{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO port_allocations(id,node_id,protocol,port,owner_type,owner_id)
VALUES ($1,$2,$3,$4,'forward_rule',$5)`, allocationID, rule.IngressNodeID, protocol, rule.IngressPort, rule.ID); err != nil {
			return Rule{}, fmt.Errorf("reserve forward port: %w", mapDatabaseError(err))
		}
	}
	if err := recordRuleChange(ctx, tx, rule, ownerID, "create", requestID, nil); err != nil {
		return Rule{}, err
	}
	if err := recheckMembership(ctx, tx, membershipID); err != nil {
		return Rule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Rule{}, fmt.Errorf("commit forward rule creation: %w", mapDatabaseError(err))
	}
	return rule, nil
}

func physicalProtocols(protocol string) []string {
	if protocol == "BOTH" {
		return []string{"TCP", "UDP"}
	}
	return []string{protocol}
}

func requireTargetPolicies(ctx context.Context, tx pgx.Tx, kind string, groupID *string, protocol string, port int) error {
	for _, physical := range physicalProtocols(protocol) {
		var policyID string
		err := tx.QueryRow(ctx, `SELECT id::text FROM forward_target_policies
WHERE kind=$1 AND target_group_id IS NOT DISTINCT FROM $2::uuid AND protocol=$3
AND port_start<=$4 AND port_end>=$4 AND enabled
ORDER BY id LIMIT 1 FOR SHARE`, kind, groupID, physical, port).Scan(&policyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPolicyDenied
		}
		if err != nil {
			return fmt.Errorf("check forward target policy: %w", err)
		}
	}
	return nil
}

func currentEntitlement(ctx context.Context, tx pgx.Tx, ownerID string) (string, entitlementSnapshot, error) {
	var membershipID string
	var snapshotJSON []byte
	err := tx.QueryRow(ctx, `SELECT id::text,snapshot_json FROM memberships
WHERE user_id=$1 AND status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp() FOR SHARE`, ownerID).Scan(&membershipID, &snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", entitlementSnapshot{}, ErrNotFound
	}
	if err != nil {
		return "", entitlementSnapshot{}, fmt.Errorf("find forward entitlement: %w", err)
	}
	var snapshot entitlementSnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		return "", entitlementSnapshot{}, fmt.Errorf("decode forward entitlement: %w", err)
	}
	return membershipID, snapshot, nil
}

func recheckMembership(ctx context.Context, tx pgx.Tx, membershipID string) error {
	var active bool
	if err := tx.QueryRow(ctx, `SELECT status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp()
FROM memberships WHERE id=$1`, membershipID).Scan(&active); err != nil {
		return fmt.Errorf("recheck forward entitlement: %w", err)
	}
	if !active {
		return ErrNotFound
	}
	return nil
}

func lockIngress(ctx context.Context, tx pgx.Tx, nodeID string, port int) (string, error) {
	var groupID string
	var nodeEnabled, groupEnabled bool
	var capabilities []string
	var proxyPort *int
	err := tx.QueryRow(ctx, `SELECT n.group_id::text,n.enabled,g.enabled,n.capabilities,n.proxy_port
FROM nodes n JOIN resource_groups g ON g.id=n.group_id WHERE n.id=$1 FOR SHARE OF n,g`, nodeID).Scan(
		&groupID, &nodeEnabled, &groupEnabled, &capabilities, &proxyPort)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock forward ingress: %w", err)
	}
	if !nodeEnabled || !groupEnabled || !slices.Contains(capabilities, "forward") || (proxyPort != nil && *proxyPort == port) {
		return "", ErrNotFound
	}
	return groupID, nil
}

func lockTargetNode(ctx context.Context, tx pgx.Tx, nodeID string) (string, error) {
	var groupID string
	var nodeEnabled, groupEnabled bool
	err := tx.QueryRow(ctx, `SELECT n.group_id::text,n.enabled,g.enabled
FROM nodes n JOIN resource_groups g ON g.id=n.group_id WHERE n.id=$1 FOR SHARE OF n,g`, nodeID).Scan(
		&groupID, &nodeEnabled, &groupEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock forward target: %w", err)
	}
	if !nodeEnabled || !groupEnabled {
		return "", ErrNotFound
	}
	return groupID, nil
}

func mapDatabaseError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23503":
			return ErrNotFound
		}
	}
	return err
}

const ruleSelect = `SELECT id::text,user_id::text,name,ingress_node_id::text,ingress_port,
target_node_id::text,target_host,target_port,line_id::text,protocol,enabled,apply_status,
created_at,updated_at FROM forward_rules`

func scanRule(row pgx.Row) (Rule, error) {
	var rule Rule
	err := row.Scan(&rule.ID, &rule.UserID, &rule.Name, &rule.IngressNodeID,
		&rule.IngressPort, &rule.TargetNodeID, &rule.TargetHost, &rule.TargetPort,
		&rule.LineID, &rule.Protocol, &rule.Enabled, &rule.ApplyStatus,
		&rule.CreatedAt, &rule.UpdatedAt)
	return rule, err
}

func (r *PostgresRepository) GetOwnRule(ctx context.Context, ownerID, ruleID string) (Rule, error) {
	rule, err := scanRule(r.pool.QueryRow(ctx, ruleSelect+` WHERE id=$1 AND user_id=$2`, ruleID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	if err != nil {
		return Rule{}, fmt.Errorf("get forward rule: %w", err)
	}
	return rule, nil
}

func (r *PostgresRepository) ListOwnRules(ctx context.Context, ownerID string, limit int, afterID string) ([]Rule, error) {
	rows, err := r.pool.Query(ctx, ruleSelect+` WHERE user_id=$1 AND id::text>$3 ORDER BY id::text LIMIT $2`, ownerID, limit, afterID)
	if err != nil {
		return nil, fmt.Errorf("list forward rules: %w", err)
	}
	defer rows.Close()
	rules := make([]Rule, 0)
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan forward rule: %w", err)
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate forward rules: %w", err)
	}
	return rules, nil
}

func recordRuleChange(ctx context.Context, tx pgx.Tx, rule Rule, actorID, action, requestID string, before *Rule) error {
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
		encoded, err := json.Marshal(rule)
		if err != nil {
			return err
		}
		afterJSON = string(encoded)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id)
VALUES ($1,$2,$3,'forward_rule',$4,$5,$6,$7)`, auditID, actorID, action, rule.ID, beforeJSON, afterJSON, requestID); err != nil {
		return fmt.Errorf("audit forward rule: %w", err)
	}
	payload := map[string]string{"node_id": rule.IngressNodeID, "rule_id": rule.ID, "action": action}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
VALUES ($1,'forward_rule.changed',$2,$3,$4)`, outboxID, rule.ID, payload, "forward:"+outboxID); err != nil {
		return fmt.Errorf("queue forward rule change: %w", err)
	}
	return nil
}
