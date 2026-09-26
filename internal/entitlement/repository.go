package entitlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("entitlement resource not found")
	ErrConflict = errors.New("entitlement resource conflict")
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func entitlementError(err error) error {
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

const planSelect = `SELECT p.id::text,p.name,p.billing_mode,p.period_months,p.quota_bytes,
p.default_multiplier_milli,p.status,p.created_at,
l.max_forward_rules_per_node,l.max_subscriptions,l.max_routing_rules,
l.allow_custom_lines,l.max_custom_lines,l.max_hops,
ARRAY(SELECT g.resource_group_id::text FROM plan_resource_group_grants g
  WHERE g.plan_id=p.id AND g.allowed ORDER BY g.resource_group_id::text),
ARRAY(SELECT g.line_id::text FROM plan_line_grants g
  WHERE g.plan_id=p.id AND g.allowed ORDER BY g.line_id::text)
FROM plans p JOIN plan_limits l ON l.plan_id=p.id WHERE p.id=$1`

type planQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadPlan(ctx context.Context, q planQueryer, planID string) (Plan, error) {
	var plan Plan
	err := q.QueryRow(ctx, planSelect, planID).Scan(&plan.ID, &plan.Name, &plan.BillingMode,
		&plan.PeriodMonths, &plan.QuotaBytes, &plan.DefaultMultiplierMilli, &plan.Status,
		&plan.CreatedAt, &plan.Limits.MaxForwardRulesPerNode, &plan.Limits.MaxSubscriptions,
		&plan.Limits.MaxRoutingRules, &plan.Limits.AllowCustomLines, &plan.Limits.MaxCustomLines,
		&plan.Limits.MaxHops, &plan.ResourceGroupIDs, &plan.LineIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, ErrNotFound
	}
	if err != nil {
		return Plan{}, fmt.Errorf("load plan: %w", err)
	}
	return plan, nil
}

func (r *PostgresRepository) CreatePlan(ctx context.Context, input PlanInput, actorID, requestID string) (Plan, error) {
	planID, err := id.NewV7()
	if err != nil {
		return Plan{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return Plan{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("begin plan creation: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO plans(id,name,quota_bytes,default_multiplier_milli)
VALUES ($1,$2,$3,$4)`, planID, input.Name, input.QuotaBytes, input.DefaultMultiplierMilli); err != nil {
		return Plan{}, fmt.Errorf("insert plan: %w", entitlementError(err))
	}
	limits := input.Limits
	if _, err := tx.Exec(ctx, `INSERT INTO plan_limits(plan_id,max_forward_rules_per_node,
max_subscriptions,max_routing_rules,allow_custom_lines,max_custom_lines,max_hops)
VALUES ($1,$2,$3,$4,$5,$6,$7)`, planID, limits.MaxForwardRulesPerNode, limits.MaxSubscriptions,
		limits.MaxRoutingRules, limits.AllowCustomLines, limits.MaxCustomLines, limits.MaxHops); err != nil {
		return Plan{}, fmt.Errorf("insert plan limits: %w", entitlementError(err))
	}
	for _, groupID := range input.ResourceGroupIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO plan_resource_group_grants(plan_id,resource_group_id)
VALUES ($1,$2)`, planID, groupID); err != nil {
			return Plan{}, fmt.Errorf("grant plan resource group: %w", entitlementError(err))
		}
	}
	for _, lineID := range input.LineIDs {
		var ownerID *string
		var lineEnabled bool
		err := tx.QueryRow(ctx, `SELECT owner_user_id::text,enabled FROM lines WHERE id=$1 FOR SHARE`, lineID).Scan(&ownerID, &lineEnabled)
		if errors.Is(err, pgx.ErrNoRows) {
			return Plan{}, ErrNotFound
		}
		if err != nil {
			return Plan{}, fmt.Errorf("load grant line: %w", err)
		}
		if ownerID != nil || !lineEnabled {
			return Plan{}, ErrNotFound
		}
		var hopCount int
		var allowed bool
		err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_and(
  h.position=0 AND h.role='egress' AND n.enabled AND g.enabled
  AND 'proxy'=ANY(n.capabilities) AND n.group_id::text=ANY($2::text[])),false)
FROM line_hops h JOIN nodes n ON n.id=h.node_id
JOIN resource_groups g ON g.id=n.group_id WHERE h.line_id=$1`, lineID, input.ResourceGroupIDs).Scan(&hopCount, &allowed)
		if err != nil {
			return Plan{}, fmt.Errorf("validate grant line hops: %w", err)
		}
		if hopCount != 1 || !allowed {
			return Plan{}, ValidationError{"line_ids", "shared line must have one enabled authorized proxy hop"}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO plan_line_grants(plan_id,line_id)
VALUES ($1,$2)`, planID, lineID); err != nil {
			return Plan{}, fmt.Errorf("grant plan line: %w", entitlementError(err))
		}
	}
	plan, err := loadPlan(ctx, tx, planID)
	if err != nil {
		return Plan{}, err
	}
	after, err := json.Marshal(plan)
	if err != nil {
		return Plan{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,'create','plan',$3,$4,$5)`, auditID, actorID, planID, string(after), requestID); err != nil {
		return Plan{}, fmt.Errorf("audit plan: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Plan{}, fmt.Errorf("commit plan: %w", entitlementError(err))
	}
	return plan, nil
}

func (r *PostgresRepository) ListPlans(ctx context.Context, limit int, afterID string) ([]Plan, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text FROM plans WHERE id::text > $2 ORDER BY id::text LIMIT $1`, limit, afterID)
	if err != nil {
		return nil, fmt.Errorf("list plan IDs: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var planID string
		if err := rows.Scan(&planID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan plan ID: %w", err)
		}
		ids = append(ids, planID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate plan IDs: %w", err)
	}
	rows.Close()
	plans := make([]Plan, 0, len(ids))
	for _, planID := range ids {
		plan, err := loadPlan(ctx, r.pool, planID)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func (r *PostgresRepository) CreateMembership(ctx context.Context, input MembershipInput, actorID, requestID string) (Membership, error) {
	membershipID, err := id.NewV7()
	if err != nil {
		return Membership{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return Membership{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Membership{}, fmt.Errorf("begin membership creation: %w", err)
	}
	defer tx.Rollback(ctx)
	var lockedUserID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE id=$1 AND status='active' FOR UPDATE`, input.UserID).Scan(&lockedUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("lock membership user: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE memberships SET status='expired'
WHERE user_id=$1 AND status='active' AND ends_at<=now()`, input.UserID); err != nil {
		return Membership{}, fmt.Errorf("expire old memberships: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships
WHERE user_id=$1 AND status='active')`, input.UserID).Scan(&exists); err != nil {
		return Membership{}, fmt.Errorf("check current membership: %w", err)
	}
	if exists {
		return Membership{}, ErrConflict
	}
	plan, err := loadPlan(ctx, tx, input.PlanID)
	if err != nil {
		return Membership{}, err
	}
	if plan.Status != "active" {
		return Membership{}, ErrConflict
	}
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return Membership{}, fmt.Errorf("read membership database time: %w", err)
	}
	if input.StartsAt.After(databaseNow) {
		return Membership{}, ValidationError{"starts_at", "membership has not started"}
	}
	if !input.EndsAt.After(input.StartsAt) || !input.EndsAt.After(databaseNow.Add(5*time.Minute)) {
		return Membership{}, ValidationError{"ends_at", "membership term is too close to expiry"}
	}
	membership := Membership{ID: membershipID, UserID: input.UserID, PlanID: input.PlanID,
		StartsAt: input.StartsAt, EndsAt: input.EndsAt, Status: "active",
		AnchorDay: input.AnchorDay, Timezone: input.Timezone, Snapshot: plan.Snapshot()}
	snapshotJSON, err := json.Marshal(membership.Snapshot)
	if err != nil {
		return Membership{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,
anchor_day,timezone,snapshot_json,period_months) VALUES ($1,$2,$3,$4,$5,'active',$6,$7,$8,$9)
RETURNING created_at`, membership.ID, membership.UserID, membership.PlanID,
		membership.StartsAt, membership.EndsAt, membership.AnchorDay,
		membership.Timezone, string(snapshotJSON), plan.PeriodMonths).Scan(&membership.CreatedAt)
	if err != nil {
		return Membership{}, fmt.Errorf("insert membership: %w", entitlementError(err))
	}
	after, err := json.Marshal(membership)
	if err != nil {
		return Membership{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,'create','membership',$3,$4,$5)`, auditID, actorID, membership.ID, string(after), requestID); err != nil {
		return Membership{}, fmt.Errorf("audit membership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Membership{}, fmt.Errorf("commit membership: %w", entitlementError(err))
	}
	return membership, nil
}

func (r *PostgresRepository) GetCurrentMembership(ctx context.Context, userID string) (Membership, error) {
	var membership Membership
	var snapshotJSON []byte
	err := r.pool.QueryRow(ctx, `SELECT id::text,user_id::text,plan_id::text,starts_at,ends_at,
status,anchor_day,timezone,snapshot_json,created_at FROM memberships
WHERE user_id=$1 AND status='active' AND starts_at<=now() AND ends_at>now()`, userID).Scan(
		&membership.ID, &membership.UserID, &membership.PlanID, &membership.StartsAt,
		&membership.EndsAt, &membership.Status, &membership.AnchorDay, &membership.Timezone,
		&snapshotJSON, &membership.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("get current membership: %w", err)
	}
	if err := json.Unmarshal(snapshotJSON, &membership.Snapshot); err != nil {
		return Membership{}, fmt.Errorf("decode membership snapshot: %w", err)
	}
	return membership, nil
}
