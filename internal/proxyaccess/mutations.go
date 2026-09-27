package proxyaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

func (r *PostgresRepository) RevealOwn(ctx context.Context, ownerID, accessID string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var lineID string
	err = tx.QueryRow(ctx, `SELECT line_id::text FROM proxy_accesses WHERE id=$1 AND user_id=$2`, accessID, ownerID).Scan(&lineID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find proxy credential: %w", err)
	}
	if _, err := authorizeProxyLine(ctx, tx, ownerID, lineID); err != nil {
		return "", err
	}
	var ciphertext string
	err = tx.QueryRow(ctx, `SELECT credential_ciphertext FROM proxy_accesses WHERE id=$1 AND user_id=$2 FOR SHARE`, accessID, ownerID).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read proxy credential: %w", err)
	}
	credential, err := r.cipher.Open(accessID, ownerID, ciphertext)
	if err != nil {
		return "", fmt.Errorf("decrypt proxy credential: %w", err)
	}
	return credential, nil
}

func (r *PostgresRepository) RotateOwn(ctx context.Context, ownerID, accessID, requestID string) (string, error) {
	credential, err := GenerateCredential()
	if err != nil {
		return "", err
	}
	sealed, err := r.cipher.Seal(accessID, ownerID, credential)
	if err != nil {
		return "", err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var lineID string
	err = tx.QueryRow(ctx, `SELECT line_id::text FROM proxy_accesses WHERE id=$1 AND user_id=$2`, accessID, ownerID).Scan(&lineID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find proxy access for rotation: %w", err)
	}
	membershipID, err := authorizeProxyLine(ctx, tx, ownerID, lineID)
	if err != nil {
		return "", err
	}
	before, err := scanAccess(tx.QueryRow(ctx, accessSelect+` WHERE id=$1 AND user_id=$2 FOR UPDATE`, accessID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock proxy access for rotation: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE proxy_accesses SET credential_hash=$3,credential_ciphertext=$4,
apply_status='pending',updated_at=clock_timestamp() WHERE id=$1 AND user_id=$2`, accessID, ownerID,
		TrojanDigest(credential), sealed); err != nil {
		return "", fmt.Errorf("rotate proxy access: %w", proxyDatabaseError(err))
	}
	if err := recordAccessChange(ctx, tx, before, ownerID, "rotate", requestID); err != nil {
		return "", err
	}
	if err := recheckAccessMembership(ctx, tx, membershipID); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit proxy rotation: %w", proxyDatabaseError(err))
	}
	return credential, nil
}

func (r *PostgresRepository) UpdateOwn(ctx context.Context, ownerID, accessID string, patch AccessPatch, requestID string) (Access, error) {
	patch, err := NormalizeAccessPatch(patch)
	if err != nil {
		return Access{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Access{}, err
	}
	defer tx.Rollback(ctx)
	var userStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, ownerID).Scan(&userStatus)
	if errors.Is(err, pgx.ErrNoRows) || userStatus != "active" {
		return Access{}, ErrNotFound
	}
	if err != nil {
		return Access{}, err
	}
	before, err := scanAccess(tx.QueryRow(ctx, accessSelect+` WHERE id=$1 AND user_id=$2 FOR UPDATE`, accessID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Access{}, ErrNotFound
	}
	if err != nil {
		return Access{}, err
	}
	var membershipID string
	if patch.Enabled != nil && *patch.Enabled {
		membershipID, err = authorizeProxyLine(ctx, tx, ownerID, before.LineID)
		if err != nil {
			return Access{}, err
		}
	}
	name, enabled := before.Name, before.Enabled
	if patch.Name != nil {
		name = *patch.Name
	}
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	if _, err := tx.Exec(ctx, `UPDATE proxy_accesses SET name=$3,enabled=$4,
apply_status='pending',updated_at=clock_timestamp() WHERE id=$1 AND user_id=$2`, accessID, ownerID, name, enabled); err != nil {
		return Access{}, fmt.Errorf("update proxy access: %w", proxyDatabaseError(err))
	}
	after, err := scanAccess(tx.QueryRow(ctx, accessSelect+` WHERE id=$1 AND user_id=$2`, accessID, ownerID))
	if err != nil {
		return Access{}, fmt.Errorf("read updated proxy access: %w", proxyDatabaseError(err))
	}
	if err := recordAccessChange(ctx, tx, after, ownerID, "update", requestID); err != nil {
		return Access{}, err
	}
	if membershipID != "" {
		if err := recheckAccessMembership(ctx, tx, membershipID); err != nil {
			return Access{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Access{}, fmt.Errorf("commit proxy access update: %w", proxyDatabaseError(err))
	}
	return after, nil
}

func (r *PostgresRepository) DeleteOwn(ctx context.Context, ownerID, accessID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedOwnerID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, ownerID).Scan(&lockedOwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock proxy access owner for deletion: %w", err)
	}
	before, err := scanAccess(tx.QueryRow(ctx, accessSelect+` WHERE id=$1 AND user_id=$2 FOR UPDATE`, accessID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM proxy_accesses WHERE id=$1 AND user_id=$2`, accessID, ownerID); err != nil {
		return fmt.Errorf("delete proxy access: %w", proxyDatabaseError(err))
	}
	if err := recordAccessChange(ctx, tx, before, ownerID, "delete", requestID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit proxy access deletion: %w", proxyDatabaseError(err))
	}
	return nil
}

func recheckAccessMembership(ctx context.Context, tx pgx.Tx, membershipID string) error {
	var active bool
	if err := tx.QueryRow(ctx, `SELECT status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp()
FROM memberships WHERE id=$1`, membershipID).Scan(&active); err != nil {
		return fmt.Errorf("recheck proxy access membership: %w", err)
	}
	if !active {
		return ErrNotFound
	}
	return nil
}

func recordAccessChange(ctx context.Context, tx pgx.Tx, access Access, ownerID, action, requestID string) error {
	auditID, err := id.NewV7()
	if err != nil {
		return err
	}
	value, err := json.Marshal(access)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,$3,'proxy_access',$4,$5,$6)`, auditID, ownerID, action, access.ID, value, requestID); err != nil {
		return fmt.Errorf("audit proxy access change: %w", err)
	}
	var nodeID string
	if err := tx.QueryRow(ctx, `SELECT node_id::text FROM line_hops WHERE line_id=$1 AND position=0`, access.LineID).Scan(&nodeID); err != nil {
		return fmt.Errorf("resolve proxy access Agent node: %w", err)
	}
	payload, err := json.Marshal(struct {
		NodeID string `json:"node_id"`
	}{NodeID: nodeID})
	if err != nil {
		return err
	}
	eventID, err := id.NewV7()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
VALUES ($1,'proxy_access.changed',$2,$3,$4)`, eventID, access.ID, payload, eventID); err != nil {
		return fmt.Errorf("enqueue proxy access convergence: %w", err)
	}
	return nil
}
