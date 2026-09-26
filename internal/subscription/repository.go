package subscription

import (
	"context"
	"controlplane/internal/catalog"
	"controlplane/internal/entitlement"
	"controlplane/internal/platform/id"
	"controlplane/internal/proxyaccess"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"time"
)

type PostgresRepository struct {
	pool   *pgxpool.Pool
	cipher *proxyaccess.CredentialCipher
}

func NewPostgresRepository(pool *pgxpool.Pool, cipher *proxyaccess.CredentialCipher) *PostgresRepository {
	return &PostgresRepository{pool: pool, cipher: cipher}
}

const subscriptionSelect = `SELECT s.id::text,s.user_id::text,s.name,s.name_template,s.enabled,s.created_at,s.updated_at,
ARRAY(SELECT t.proxy_access_id::text FROM subscription_proxy_targets t WHERE t.subscription_id=s.id ORDER BY t.sort_order) FROM subscriptions s`

func scanSubscription(row pgx.Row) (Subscription, error) {
	var s Subscription
	err := row.Scan(&s.ID, &s.UserID, &s.Name, &s.NameTemplate, &s.Enabled, &s.CreatedAt, &s.UpdatedAt, &s.ProxyAccessIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, ErrNotFound
	}
	return s, err
}
func (r *PostgresRepository) GetOwn(ctx context.Context, owner, subID string) (Subscription, error) {
	return scanSubscription(r.pool.QueryRow(ctx, subscriptionSelect+` WHERE s.id=$1 AND s.user_id=$2`, subID, owner))
}
func (r *PostgresRepository) ListOwn(ctx context.Context, owner string, limit int, after string) ([]Subscription, error) {
	rows, err := r.pool.Query(ctx, subscriptionSelect+` WHERE s.user_id=$1 AND s.id::text>$3 ORDER BY s.id::text LIMIT $2`, owner, limit, after)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()
	items := make([]Subscription, 0)
	for rows.Next() {
		s, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, s)
	}
	return items, rows.Err()
}
func (r *PostgresRepository) Create(ctx context.Context, owner string, in Input, requestID string) (Subscription, string, error) {
	owner = strings.ToLower(owner)
	if !catalog.ValidID(owner) {
		return Subscription{}, "", ErrNotFound
	}
	in, err := Normalize(NewSubscription{Name: in.Name, NameTemplate: in.NameTemplate, ProxyAccessIDs: in.ProxyAccessIDs, Enabled: &in.Enabled})
	if err != nil {
		return Subscription{}, "", err
	}
	subID, err := id.NewV7()
	if err != nil {
		return Subscription{}, "", err
	}
	token, err := GenerateToken()
	if err != nil {
		return Subscription{}, "", err
	}
	hash, _ := TokenHash(token)
	sealed, err := sealToken(r.cipher, subID, owner, token)
	if err != nil {
		return Subscription{}, "", err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Subscription{}, "", err
	}
	defer tx.Rollback(ctx)
	if err := lockOwner(ctx, tx, owner); err != nil {
		return Subscription{}, "", err
	}
	grant, ends, err := loadGrant(ctx, tx, owner)
	if err != nil {
		return Subscription{}, "", err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1`, owner).Scan(&count); err != nil {
		return Subscription{}, "", err
	}
	if count >= grant.Limits.MaxSubscriptions {
		return Subscription{}, "", ErrLimit
	}
	if err := authorizeTargets(ctx, tx, owner, in.ProxyAccessIDs, grant); err != nil {
		return Subscription{}, "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO subscriptions(id,user_id,name,name_template,enabled,token_hash,token_ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7)`, subID, owner, in.Name, in.NameTemplate, in.Enabled, hash, sealed)
	if err != nil {
		return Subscription{}, "", databaseError(err)
	}
	if err := replaceTargets(ctx, tx, subID, owner, in.ProxyAccessIDs); err != nil {
		return Subscription{}, "", err
	}
	sub, err := scanSubscription(tx.QueryRow(ctx, subscriptionSelect+` WHERE s.id=$1`, subID))
	if err != nil {
		return Subscription{}, "", err
	}
	if err := audit(ctx, tx, sub, "create", requestID); err != nil {
		return Subscription{}, "", err
	}
	if err := checkExpiry(ctx, tx, ends); err != nil {
		return Subscription{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return Subscription{}, "", databaseError(err)
	}
	return sub, token, nil
}
func lockOwner(ctx context.Context, tx pgx.Tx, owner string) error {
	var active bool
	err := tx.QueryRow(ctx, `SELECT status='active' FROM users WHERE id=$1 FOR UPDATE`, owner).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !active {
		return ErrNotFound
	}
	return err
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadGrant(ctx context.Context, q queryRower, owner string) (entitlement.Snapshot, time.Time, error) {
	var snapshot entitlement.Snapshot
	var raw []byte
	var ends time.Time
	err := q.QueryRow(ctx, `SELECT m.snapshot_json,m.ends_at FROM memberships m JOIN users u ON u.id=m.user_id
WHERE m.user_id=$1 AND u.status='active' AND m.status='active' AND m.starts_at<=clock_timestamp() AND m.ends_at>clock_timestamp()`, owner).Scan(&raw, &ends)
	if errors.Is(err, pgx.ErrNoRows) {
		return snapshot, ends, ErrNotFound
	}
	if err != nil {
		return snapshot, ends, err
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return snapshot, ends, fmt.Errorf("decode subscription grant: %w", err)
	}
	return snapshot, ends, nil
}
func checkExpiry(ctx context.Context, tx pgx.Tx, ends time.Time) error {
	var live bool
	if err := tx.QueryRow(ctx, `SELECT $1::timestamptz>clock_timestamp()`, ends).Scan(&live); err != nil {
		return err
	}
	if !live {
		return ErrNotFound
	}
	return nil
}
func replaceTargets(ctx context.Context, tx pgx.Tx, subID, owner string, ids []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM subscription_proxy_targets WHERE subscription_id=$1`, subID); err != nil {
		return err
	}
	for i, target := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO subscription_proxy_targets(subscription_id,user_id,proxy_access_id,sort_order) VALUES($1,$2,$3,$4)`, subID, owner, target, i); err != nil {
			return databaseError(err)
		}
	}
	return nil
}
func audit(ctx context.Context, tx pgx.Tx, s Subscription, action, requestID string) error {
	auditID, err := id.NewV7()
	if err != nil {
		return err
	}
	safe, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id) VALUES($1,$2,$3,'subscription',$4,$5,$6)`, auditID, s.UserID, action, s.ID, safe, requestID)
	return err
}
func databaseError(err error) error {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch pgerr.Code {
		case "23505":
			return ErrConflict
		case "23503":
			return ErrNotFound
		}
	}
	return err
}
