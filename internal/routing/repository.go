package routing

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"controlplane/internal/entitlement"
	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("routing resource not found")
	ErrConflict = errors.New("routing resource conflict")
	ErrLimit    = errors.New("routing rule limit exceeded")
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

const profileSelect = `SELECT id::text,user_id::text,name,fallback_kind,fallback_line_id::text,enabled,revision,created_at,updated_at FROM routing_profiles`
const ruleSelect = `SELECT id::text,profile_id::text,priority,match_type,match_value,action,line_id::text,enabled,created_at,updated_at FROM routing_rules`

func scanProfile(row pgx.Row) (Profile, error) {
	var v Profile
	var line *string
	err := row.Scan(&v.ID, &v.UserID, &v.Name, &v.FallbackKind, &line, &v.Enabled, &v.Revision, &v.CreatedAt, &v.UpdatedAt)
	v.FallbackLineID = line
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return v, err
}
func scanRule(row pgx.Row) (Rule, error) {
	var v Rule
	var line *string
	err := row.Scan(&v.ID, &v.ProfileID, &v.Priority, &v.MatchType, &v.MatchValue, &v.Action, &line, &v.Enabled, &v.CreatedAt, &v.UpdatedAt)
	v.LineID = line
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	return v, err
}

func (r *PostgresRepository) CreateProfile(ctx context.Context, owner string, in ProfileInput, requestID string) (Profile, error) {
	in, err := NormalizeProfile(NewProfile{Name: in.Name, FallbackKind: in.FallbackKind, FallbackLineID: in.FallbackLineID, Enabled: &in.Enabled})
	if err != nil {
		return Profile{}, err
	}
	idv, err := id.NewV7()
	if err != nil {
		return Profile{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback(ctx)
	grant, err := lockGrant(ctx, tx, owner)
	if err != nil {
		return Profile{}, err
	}
	if in.FallbackKind == "line" && (in.FallbackLineID == nil || !lineAllowedTx(ctx, tx, owner, *in.FallbackLineID, grant)) {
		return Profile{}, ErrNotFound
	}
	var p Profile
	err = tx.QueryRow(ctx, `INSERT INTO routing_profiles(id,user_id,name,fallback_kind,fallback_line_id,enabled) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text,user_id::text,name,fallback_kind,fallback_line_id::text,enabled,revision,created_at,updated_at`, idv, owner, in.Name, in.FallbackKind, in.FallbackLineID, in.Enabled).Scan(&p.ID, &p.UserID, &p.Name, &p.FallbackKind, &p.FallbackLineID, &p.Enabled, &p.Revision, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Profile{}, mapError(err)
	}
	if err := audit(ctx, tx, owner, "create", "routing_profile", p.ID, p, requestID); err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, mapError(err)
	}
	return p, nil
}

func (r *PostgresRepository) ListOwnProfiles(ctx context.Context, owner string, limit int, after string) ([]Profile, error) {
	rows, err := r.pool.Query(ctx, profileSelect+` WHERE user_id=$1 AND id::text>$2 ORDER BY id::text LIMIT $3`, owner, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		v, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *PostgresRepository) GetOwnProfile(ctx context.Context, owner, profileID string) (Profile, error) {
	return scanProfile(r.pool.QueryRow(ctx, profileSelect+` WHERE user_id=$1 AND id=$2`, owner, profileID))
}
func (r *PostgresRepository) UpdateProfile(ctx context.Context, owner, profileID string, patch ProfilePatch, requestID string) (Profile, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback(ctx)
	grant, err := lockGrant(ctx, tx, owner)
	if err != nil {
		return Profile{}, err
	}
	before, err := scanProfile(tx.QueryRow(ctx, profileSelect+` WHERE user_id=$1 AND id=$2 FOR UPDATE`, owner, profileID))
	if err != nil {
		return Profile{}, err
	}
	name := before.Name
	if patch.Name != nil {
		name = *patch.Name
	}
	kind := before.FallbackKind
	if patch.FallbackKind != nil {
		kind = *patch.FallbackKind
	}
	line := before.FallbackLineID
	if patch.FallbackLineID != nil {
		line = patch.FallbackLineID
	}
	if patch.FallbackKind != nil && *patch.FallbackKind != "line" && patch.FallbackLineID == nil {
		line = nil
	}
	enabled := before.Enabled
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	normalized, err := NormalizeProfile(NewProfile{Name: name, FallbackKind: kind, FallbackLineID: line, Enabled: &enabled})
	if err != nil {
		return Profile{}, err
	}
	if normalized.FallbackLineID != nil && !lineAllowedTx(ctx, tx, owner, *normalized.FallbackLineID, grant) {
		return Profile{}, ErrNotFound
	}
	var after Profile
	err = tx.QueryRow(ctx, `UPDATE routing_profiles SET name=$3,fallback_kind=$4,fallback_line_id=$5,enabled=$6,revision=revision+1,updated_at=now() WHERE user_id=$1 AND id=$2 RETURNING id::text,user_id::text,name,fallback_kind,fallback_line_id::text,enabled,revision,created_at,updated_at`, owner, profileID, normalized.Name, normalized.FallbackKind, normalized.FallbackLineID, normalized.Enabled).Scan(&after.ID, &after.UserID, &after.Name, &after.FallbackKind, &after.FallbackLineID, &after.Enabled, &after.Revision, &after.CreatedAt, &after.UpdatedAt)
	if err != nil {
		return Profile{}, mapError(err)
	}
	if err := audit(ctx, tx, owner, "update", "routing_profile", after.ID, after, requestID); err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, mapError(err)
	}
	return after, nil
}
func (r *PostgresRepository) DeleteProfile(ctx context.Context, owner, profileID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	p, err := scanProfile(tx.QueryRow(ctx, profileSelect+` WHERE user_id=$1 AND id=$2 FOR UPDATE`, owner, profileID))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM routing_profiles WHERE user_id=$1 AND id=$2`, owner, profileID); err != nil {
		return mapError(err)
	}
	if err := audit(ctx, tx, owner, "delete", "routing_profile", p.ID, p, requestID); err != nil {
		return err
	}
	return mapError(tx.Commit(ctx))
}

func (r *PostgresRepository) ListRules(ctx context.Context, owner, profileID string, limit int, after string) ([]Rule, error) {
	if _, err := r.GetOwnProfile(ctx, owner, profileID); err != nil {
		return nil, err
	}
	priority, cursorID, err := ParseRuleCursor(after)
	if err != nil {
		return nil, err
	}
	var rows pgx.Rows
	if priority == 0 {
		rows, err = r.pool.Query(ctx, ruleSelect+` WHERE profile_id=$1 ORDER BY priority,id LIMIT $2`, profileID, limit)
	} else {
		rows, err = r.pool.Query(ctx, ruleSelect+` WHERE profile_id=$1 AND (priority,id) > ($2,$3::uuid) ORDER BY priority,id LIMIT $4`, profileID, priority, cursorID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		v, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *PostgresRepository) CreateRule(ctx context.Context, owner, profileID string, in RuleInput, requestID string) (Rule, error) {
	in, err := NormalizeRule(NewRule{Priority: in.Priority, MatchType: in.MatchType, MatchValue: in.MatchValue, Action: in.Action, LineID: in.LineID, Enabled: &in.Enabled})
	if err != nil {
		return Rule{}, err
	}
	rid, err := id.NewV7()
	if err != nil {
		return Rule{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Rule{}, err
	}
	defer tx.Rollback(ctx)
	grant, err := lockGrant(ctx, tx, owner)
	if err != nil {
		return Rule{}, err
	}
	if _, err := scanProfile(tx.QueryRow(ctx, profileSelect+` WHERE user_id=$1 AND id=$2 FOR UPDATE`, owner, profileID)); err != nil {
		return Rule{}, err
	}
	if in.LineID != nil && !lineAllowedTx(ctx, tx, owner, *in.LineID, grant) {
		return Rule{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, owner); err != nil {
		return Rule{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM routing_rules rr JOIN routing_profiles rp ON rp.id=rr.profile_id WHERE rp.user_id=$1`, owner).Scan(&count); err != nil {
		return Rule{}, err
	}
	if count >= grant.Limits.MaxRoutingRules {
		return Rule{}, ErrLimit
	}
	var v Rule
	err = tx.QueryRow(ctx, `INSERT INTO routing_rules(id,profile_id,priority,match_type,match_value,action,line_id,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text,profile_id::text,priority,match_type,match_value,action,line_id::text,enabled,created_at,updated_at`, rid, profileID, in.Priority, in.MatchType, in.MatchValue, in.Action, in.LineID, in.Enabled).Scan(&v.ID, &v.ProfileID, &v.Priority, &v.MatchType, &v.MatchValue, &v.Action, &v.LineID, &v.Enabled, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return Rule{}, mapError(err)
	}
	if err := audit(ctx, tx, owner, "create", "routing_rule", v.ID, v, requestID); err != nil {
		return Rule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Rule{}, mapError(err)
	}
	return v, nil
}
func (r *PostgresRepository) UpdateRule(ctx context.Context, owner, profileID, ruleID string, patch RulePatch, requestID string) (Rule, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Rule{}, err
	}
	defer tx.Rollback(ctx)
	grant, err := lockGrant(ctx, tx, owner)
	if err != nil {
		return Rule{}, err
	}
	before, err := scanRule(tx.QueryRow(ctx, ruleSelect+` WHERE id=$1 AND profile_id=$2 AND EXISTS(SELECT 1 FROM routing_profiles WHERE id=$2 AND user_id=$3) FOR UPDATE`, ruleID, profileID, owner))
	if err != nil {
		return Rule{}, err
	}
	priority := before.Priority
	if patch.Priority != nil {
		priority = *patch.Priority
	}
	action := before.Action
	if patch.Action != nil {
		action = *patch.Action
	}
	line := before.LineID
	if patch.LineID != nil {
		line = patch.LineID
	}
	if patch.Action != nil && *patch.Action != "line" && patch.LineID == nil {
		line = nil
	}
	enabled := before.Enabled
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	normalized, err := NormalizeRule(NewRule{Priority: priority, MatchType: before.MatchType, MatchValue: before.MatchValue, Action: action, LineID: line, Enabled: &enabled})
	if err != nil {
		return Rule{}, err
	}
	if normalized.LineID != nil && !lineAllowedTx(ctx, tx, owner, *normalized.LineID, grant) {
		return Rule{}, ErrNotFound
	}
	var v Rule
	err = tx.QueryRow(ctx, `UPDATE routing_rules SET priority=$3,action=$4,line_id=$5,enabled=$6,updated_at=now() WHERE id=$1 AND profile_id=$2 RETURNING id::text,profile_id::text,priority,match_type,match_value,action,line_id::text,enabled,created_at,updated_at`, ruleID, profileID, normalized.Priority, normalized.Action, normalized.LineID, normalized.Enabled).Scan(&v.ID, &v.ProfileID, &v.Priority, &v.MatchType, &v.MatchValue, &v.Action, &v.LineID, &v.Enabled, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return Rule{}, mapError(err)
	}
	if err := audit(ctx, tx, owner, "update", "routing_rule", v.ID, v, requestID); err != nil {
		return Rule{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Rule{}, mapError(err)
	}
	return v, nil
}
func (r *PostgresRepository) DeleteRule(ctx context.Context, owner, profileID, ruleID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	v, err := scanRule(tx.QueryRow(ctx, ruleSelect+` WHERE id=$1 AND profile_id=$2 AND EXISTS(SELECT 1 FROM routing_profiles WHERE id=$2 AND user_id=$3) FOR UPDATE`, ruleID, profileID, owner))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM routing_rules WHERE id=$1 AND profile_id=$2`, ruleID, profileID); err != nil {
		return mapError(err)
	}
	if err := audit(ctx, tx, owner, "delete", "routing_rule", v.ID, v, requestID); err != nil {
		return err
	}
	return mapError(tx.Commit(ctx))
}
func (r *PostgresRepository) ValidateOwnProfile(ctx context.Context, owner, profileID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	grant, err := lockGrant(ctx, tx, owner)
	if err != nil {
		return err
	}
	p, err := scanProfile(tx.QueryRow(ctx, profileSelect+` WHERE user_id=$1 AND id=$2`, owner, profileID))
	if err != nil {
		return err
	}
	if p.FallbackLineID != nil && !lineAllowedTx(ctx, tx, owner, *p.FallbackLineID, grant) {
		return ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT line_id::text FROM routing_rules WHERE profile_id=$1 AND action='line' AND line_id IS NOT NULL`, profileID)
	if err != nil {
		return err
	}
	lines := []string{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, line)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, line := range lines {
		if !lineAllowedTx(ctx, tx, owner, line, grant) {
			return ErrNotFound
		}
	}
	return nil
}

func lockGrant(ctx context.Context, tx pgx.Tx, owner string) (entitlement.Snapshot, error) {
	var raw []byte
	var s entitlement.Snapshot
	// Membership grants are read with a shared lock so line edits, which lock
	// their line before reading membership, cannot deadlock against routing edits.
	err := tx.QueryRow(ctx, `SELECT snapshot_json FROM memberships WHERE user_id=$1 AND status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp() FOR SHARE`, owner).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	return s, nil
}
func lineAllowedTx(ctx context.Context, tx pgx.Tx, owner, line string, g entitlement.Snapshot) bool {
	var lineOwner, group string
	err := tx.QueryRow(ctx, `SELECT COALESCE(owner_user_id::text,''),n.group_id::text FROM lines l JOIN line_hops h ON h.line_id=l.id AND h.position=0 AND h.role='egress' JOIN nodes n ON n.id=h.node_id JOIN resource_groups g ON g.id=n.group_id WHERE l.id=$1 AND l.enabled AND n.enabled AND g.enabled AND 'proxy'=ANY(n.capabilities) AND n.proxy_port IS NOT NULL AND NOT EXISTS(SELECT 1 FROM line_hops h2 WHERE h2.line_id=l.id AND h2.position<>0)`, line).Scan(&lineOwner, &group)
	if err != nil {
		return false
	}
	return slices.Contains(g.ResourceGroupIDs, group) && ((lineOwner == owner && g.Limits.AllowCustomLines) || lineOwner == "" && slices.Contains(g.LineIDs, line))
}
func audit(ctx context.Context, tx pgx.Tx, owner, action, kind, object string, value any, request string) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	aid, err := id.NewV7()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, aid, owner, action, kind, object, string(raw), request)
	return err
}
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		if p.Code == "23505" {
			return ErrConflict
		}
		if p.Code == "23503" {
			return ErrNotFound
		}
	}
	return err
}
