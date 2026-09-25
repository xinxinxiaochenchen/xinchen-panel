package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	_ "time/tzdata"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

type NewUser struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Timezone string `json:"timezone"`
}

type MemberInput NewUser

func NormalizeNewUser(input NewUser) (MemberInput, error) {
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	parsed, err := mail.ParseAddress(input.Email)
	if err != nil || parsed.Address != input.Email || len(input.Email) > 254 {
		return MemberInput{}, ErrInvalidInput
	}
	if len([]byte(input.Password)) < 12 || len([]byte(input.Password)) > 72 {
		return MemberInput{}, ErrInvalidInput
	}
	if input.Timezone == "" {
		input.Timezone = "Asia/Shanghai"
	}
	if input.Timezone == "Local" || input.Timezone == "Etc/Localtime" {
		return MemberInput{}, ErrInvalidInput
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return MemberInput{}, ErrInvalidInput
	}
	return MemberInput(input), nil
}

func ValidatePasswordChange(oldPassword, newPassword string) error {
	if oldPassword == "" || oldPassword == newPassword || len([]byte(newPassword)) < 12 || len([]byte(newPassword)) > 72 {
		return ErrInvalidInput
	}
	return nil
}

func (r *PostgresRepository) CreateMember(ctx context.Context, input MemberInput, actorID, requestID string) (PublicUser, error) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return PublicUser{}, fmt.Errorf("hash member password: %w", err)
	}
	userID, err := id.NewV7()
	if err != nil {
		return PublicUser{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return PublicUser{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return PublicUser{}, fmt.Errorf("begin member creation: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,status,timezone)
VALUES ($1,$2,$3,'active',$4)`, userID, input.Email, string(passwordHash), input.Timezone); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return PublicUser{}, ErrAlreadyExists
		}
		return PublicUser{}, fmt.Errorf("insert member user: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_code) VALUES ($1,'user')`, userID); err != nil {
		return PublicUser{}, fmt.Errorf("assign member role: %w", err)
	}
	user, err := loadUserByID(ctx, tx, userID)
	if err != nil {
		return PublicUser{}, err
	}
	public := user.Public()
	after, err := json.Marshal(public)
	if err != nil {
		return PublicUser{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,'create','user',$3,$4,$5)`, auditID, actorID, userID, string(after), requestID); err != nil {
		return PublicUser{}, fmt.Errorf("audit member creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicUser{}, fmt.Errorf("commit member creation: %w", err)
	}
	return public, nil
}

type accountQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadUserByID(ctx context.Context, q accountQueryer, userID string) (User, error) {
	query := `SELECT ` + userFields + ` FROM users u ` + userRoleJoins + `
WHERE u.id=$1 GROUP BY u.id`
	var user User
	err := q.QueryRow(ctx, query, userID).Scan(&user.ID, &user.Email, &user.PasswordHash,
		&user.Status, &user.Timezone, &user.Roles, &user.Permissions)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load user: %w", err)
	}
	return user, nil
}

func (r *PostgresRepository) ListUsers(ctx context.Context, limit int, afterID string) ([]PublicUser, error) {
	query := `SELECT ` + userFields + ` FROM users u ` + userRoleJoins + `
WHERE u.id::text>$2 GROUP BY u.id ORDER BY u.id::text LIMIT $1`
	rows, err := r.pool.Query(ctx, query, limit, afterID)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]PublicUser, 0)
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Email, &user.PasswordHash,
			&user.Status, &user.Timezone, &user.Roles, &user.Permissions); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user.Public())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (r *PostgresRepository) ChangePassword(ctx context.Context, userID, oldPassword, newPassword, requestID string) error {
	if err := ValidatePasswordChange(oldPassword, newPassword); err != nil {
		return err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin password change: %w", err)
	}
	defer tx.Rollback(ctx)
	var currentHash, status string
	err = tx.QueryRow(ctx, `SELECT password_hash,status FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&currentHash, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock password user: %w", err)
	}
	if status != "active" {
		return ErrUnauthenticated
	}
	if bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(oldPassword)) != nil {
		return ErrInvalidCredentials
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, userID, string(newHash)); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM browser_sessions WHERE user_id=$1`, userID); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,'change_password','user',$2,'{"sessions_revoked":true}'::jsonb,$3)`, auditID, userID, requestID); err != nil {
		return fmt.Errorf("audit password change: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password change: %w", err)
	}
	return nil
}
