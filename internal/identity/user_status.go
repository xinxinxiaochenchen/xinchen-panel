package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

// SetUserStatus changes a normal user's login and resource eligibility in one
// transaction. Administrator accounts are managed separately to preserve the
// control plane's recovery path.
func (r *PostgresRepository) SetUserStatus(ctx context.Context, userID, status, actorID, requestID string) (PublicUser, error) {
	if status != "active" && status != "disabled" {
		return PublicUser{}, ErrInvalidInput
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return PublicUser{}, fmt.Errorf("begin user status change: %w", err)
	}
	defer tx.Rollback(ctx)
	var current string
	var administrator bool
	err = tx.QueryRow(ctx, `SELECT u.status,EXISTS(SELECT 1 FROM user_roles ur WHERE ur.user_id=u.id AND ur.role_code='admin')
FROM users u WHERE u.id=$1 FOR UPDATE`, userID).Scan(&current, &administrator)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicUser{}, ErrNotFound
	}
	if err != nil {
		return PublicUser{}, fmt.Errorf("lock user status: %w", err)
	}
	if administrator || userID == actorID {
		return PublicUser{}, ErrInvalidInput
	}
	before, err := loadUserByID(ctx, tx, userID)
	if err != nil {
		return PublicUser{}, err
	}
	if current == status {
		return before.Public(), nil
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET status=$2,updated_at=clock_timestamp() WHERE id=$1`, userID, status); err != nil {
		return PublicUser{}, fmt.Errorf("update user status: %w", err)
	}
	if status == "disabled" {
		if _, err := tx.Exec(ctx, `DELETE FROM browser_sessions WHERE user_id=$1`, userID); err != nil {
			return PublicUser{}, fmt.Errorf("revoke disabled user sessions: %w", err)
		}
	}
	after, err := loadUserByID(ctx, tx, userID)
	if err != nil {
		return PublicUser{}, err
	}
	beforeJSON, err := json.Marshal(before.Public())
	if err != nil {
		return PublicUser{}, err
	}
	afterJSON, err := json.Marshal(after.Public())
	if err != nil {
		return PublicUser{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return PublicUser{}, err
	}
	eventID, err := id.NewV7()
	if err != nil {
		return PublicUser{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id)
VALUES ($1,$2,'update','user',$3,$4,$5,$6)`, auditID, actorID, userID, string(beforeJSON), string(afterJSON), requestID); err != nil {
		return PublicUser{}, fmt.Errorf("audit user status: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
VALUES ($1,'user.changed',$2::uuid,jsonb_build_object('user_id',($2::uuid)::text),$3)`, eventID, userID, "user-status:"+eventID); err != nil {
		return PublicUser{}, fmt.Errorf("queue user status convergence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicUser{}, fmt.Errorf("commit user status: %w", err)
	}
	return after.Public(), nil
}
