package georules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("managed rule-set not found")
	ErrConflict = errors.New("managed rule-set conflict")
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

type QueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const selectRuleSet = `SELECT id::text,kind,code,name,version,source,sha256,jsonb_array_length(entries),enabled,created_at,updated_at FROM routing_rule_sets`

func scanRuleSet(row pgx.Row) (RuleSet, error) {
	var out RuleSet
	if err := row.Scan(&out.ID, &out.Kind, &out.Code, &out.Name, &out.Version, &out.Source, &out.SHA256, &out.EntryCount, &out.Enabled, &out.CreatedAt, &out.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RuleSet{}, ErrNotFound
		}
		return RuleSet{}, err
	}
	return out, nil
}

func (r *PostgresRepository) List(ctx context.Context, limit int, after string) ([]RuleSet, error) {
	rows, err := r.pool.Query(ctx, selectRuleSet+` WHERE id::text>$2 ORDER BY id::text LIMIT $1`, limit, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RuleSet, 0)
	for rows.Next() {
		v, err := scanRuleSet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *PostgresRepository) Create(ctx context.Context, in Input, actorID, requestID string) (RuleSet, error) {
	ruleSetID, err := id.NewV7()
	if err != nil {
		return RuleSet{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return RuleSet{}, err
	}
	entries, err := json.Marshal(in.Entries)
	if err != nil {
		return RuleSet{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return RuleSet{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.Kind+":"+in.Code); err != nil {
		return RuleSet{}, err
	}
	if in.Enabled {
		if _, err := tx.Exec(ctx, `UPDATE routing_rule_sets SET enabled=false,updated_at=now() WHERE kind=$1 AND code=$2 AND enabled`, in.Kind, in.Code); err != nil {
			return RuleSet{}, err
		}
	}
	var v RuleSet
	if err := tx.QueryRow(ctx, `INSERT INTO routing_rule_sets(id,kind,code,name,version,source,sha256,entries,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id::text,kind,code,name,version,source,sha256,jsonb_array_length(entries),enabled,created_at,updated_at`, ruleSetID, in.Kind, in.Code, in.Name, in.Version, in.Source, in.SHA256, entries, in.Enabled).Scan(&v.ID, &v.Kind, &v.Code, &v.Name, &v.Version, &v.Source, &v.SHA256, &v.EntryCount, &v.Enabled, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return RuleSet{}, mapError(err)
	}
	afterJSON, err := json.Marshal(v)
	if err != nil {
		return RuleSet{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id) VALUES($1,$2,'create','routing_rule_set',$3,$4,$5)`, auditID, actorID, v.ID, string(afterJSON), requestID); err != nil {
		return RuleSet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RuleSet{}, mapError(err)
	}
	return v, nil
}

func (r *PostgresRepository) SetEnabled(ctx context.Context, setID string, enabled bool, actorID, requestID string) (RuleSet, error) {
	auditID, err := id.NewV7()
	if err != nil {
		return RuleSet{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return RuleSet{}, err
	}
	defer tx.Rollback(ctx)
	lookup, err := scanRuleSet(tx.QueryRow(ctx, selectRuleSet+` WHERE id=$1`, setID))
	if err != nil {
		return RuleSet{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lookup.Kind+":"+lookup.Code); err != nil {
		return RuleSet{}, err
	}
	before, err := scanRuleSet(tx.QueryRow(ctx, selectRuleSet+` WHERE id=$1 FOR UPDATE`, setID))
	if err != nil {
		return RuleSet{}, err
	}
	if enabled {
		if _, err := tx.Exec(ctx, `UPDATE routing_rule_sets SET enabled=false,updated_at=now() WHERE kind=$1 AND code=$2 AND id<>$3 AND enabled`, before.Kind, before.Code, setID); err != nil {
			return RuleSet{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE routing_rule_sets SET enabled=$2,updated_at=now() WHERE id=$1`, setID, enabled); err != nil {
		return RuleSet{}, mapError(err)
	}
	after, err := scanRuleSet(tx.QueryRow(ctx, selectRuleSet+` WHERE id=$1`, setID))
	if err != nil {
		return RuleSet{}, err
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return RuleSet{}, err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return RuleSet{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id) VALUES($1,$2,'update','routing_rule_set',$3,$4,$5,$6)`, auditID, actorID, setID, string(beforeJSON), string(afterJSON), requestID); err != nil {
		return RuleSet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RuleSet{}, mapError(err)
	}
	return after, nil
}

func LoadEnabled(ctx context.Context, q QueryRower, kind, code string) (Data, error) {
	var out Data
	var raw []byte
	err := q.QueryRow(ctx, `SELECT kind,code,version,sha256,entries FROM routing_rule_sets WHERE kind=$1 AND code=$2 AND enabled`, kind, code).Scan(&out.Kind, &out.Code, &out.Version, &out.SHA256, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Data{}, ErrNotFound
	}
	if err != nil {
		return Data{}, fmt.Errorf("load managed rule-set: %w", err)
	}
	if err := json.Unmarshal(raw, &out.Entries); err != nil {
		return Data{}, fmt.Errorf("decode managed rule-set: %w", err)
	}
	return out, nil
}

func mapError(err error) error {
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
