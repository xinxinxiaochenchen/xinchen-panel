package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

const userRoleJoins = `
LEFT JOIN user_roles ur ON ur.user_id = u.id
LEFT JOIN role_permissions rp ON rp.role_code = ur.role_code`

const userFields = `
u.id::text, u.email, u.password_hash, u.status, u.timezone,
COALESCE(array_agg(DISTINCT ur.role_code) FILTER (WHERE ur.role_code IS NOT NULL), '{}'::text[]),
COALESCE(array_agg(DISTINCT rp.permission_code) FILTER (WHERE rp.permission_code IS NOT NULL), '{}'::text[])`

func (r *PostgresRepository) FindUserByEmail(ctx context.Context, email string) (User, error) {
	query := `SELECT ` + userFields + ` FROM users u ` + userRoleJoins + `
WHERE lower(u.email) = $1 GROUP BY u.id`
	var user User
	err := r.pool.QueryRow(ctx, query, email).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Status, &user.Timezone,
		&user.Roles, &user.Permissions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("query user by email: %w", err)
	}
	return user, nil
}

func (r *PostgresRepository) InsertSession(ctx context.Context, session Session, expectedPasswordHash string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin session creation: %w", err)
	}
	defer tx.Rollback(ctx)
	var currentHash, status string
	err = tx.QueryRow(ctx, `SELECT password_hash,status FROM users WHERE id=$1 FOR UPDATE`, session.UserID).Scan(&currentHash, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (currentHash != expectedPasswordHash || status != "active")) {
		return ErrInvalidCredentials
	}
	if err != nil {
		return fmt.Errorf("lock session user: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO browser_sessions(token_hash,csrf_hash,user_id,expires_at)
VALUES ($1,$2,$3,$4)`, session.TokenHash[:], session.CSRFHash[:], session.UserID, session.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit session creation: %w", err)
	}
	return nil
}

func (r *PostgresRepository) FindSession(ctx context.Context, tokenHash [32]byte) (Session, User, error) {
	query := `SELECT s.csrf_hash, s.expires_at, ` + userFields + `
FROM browser_sessions s JOIN users u ON u.id = s.user_id ` + userRoleJoins + `
WHERE s.token_hash = $1 GROUP BY s.token_hash, u.id`
	var session Session
	var csrfHash []byte
	var user User
	err := r.pool.QueryRow(ctx, query, tokenHash[:]).Scan(
		&csrfHash, &session.ExpiresAt,
		&user.ID, &user.Email, &user.PasswordHash, &user.Status, &user.Timezone,
		&user.Roles, &user.Permissions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, User{}, ErrNotFound
	}
	if err != nil {
		return Session{}, User{}, fmt.Errorf("query session: %w", err)
	}
	if len(csrfHash) != len(session.CSRFHash) {
		return Session{}, User{}, fmt.Errorf("invalid stored CSRF hash length")
	}
	copy(session.CSRFHash[:], csrfHash)
	session.TokenHash = tokenHash
	session.UserID = user.ID
	return session, user, nil
}

func (r *PostgresRepository) DeleteSession(ctx context.Context, tokenHash [32]byte) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM browser_sessions WHERE token_hash=$1`, tokenHash[:])
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (r *PostgresRepository) DeleteExpiredSessions(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit < 1 {
		return 0, fmt.Errorf("invalid session cleanup limit")
	}
	tag, err := r.pool.Exec(ctx, `WITH expired AS (
    SELECT token_hash FROM browser_sessions
    WHERE expires_at <= $1 ORDER BY expires_at LIMIT $2 FOR UPDATE SKIP LOCKED
)
DELETE FROM browser_sessions AS s USING expired WHERE s.token_hash = expired.token_hash`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}
