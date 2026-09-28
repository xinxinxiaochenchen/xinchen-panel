package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrAlreadyExists = errors.New("user already exists")
	ErrInvalidInput  = errors.New("invalid admin bootstrap input")
)

// AdminConfigured reports whether initial administrator setup has completed.
func AdminConfigured(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var configured bool
	err := pool.QueryRow(ctx, adminInitializedQuery).Scan(&configured)
	return configured, err
}

func BootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, email, password string) (PublicUser, error) {
	user, _, err := bootstrapAdmin(ctx, pool, email, password, false, "admin-bootstrap")
	return user, err
}

// BootstrapAdminIfNeeded creates only the first administrator. Repeated
// or concurrent installer runs never reset an existing administrator password.
func BootstrapAdminIfNeeded(ctx context.Context, pool *pgxpool.Pool, email, password string) (PublicUser, bool, error) {
	return bootstrapAdmin(ctx, pool, email, password, true, "admin-bootstrap")
}

func ValidateAdminCredentials(email, password string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) > 254 || len(password) < 12 || len(password) > 72 {
		return ErrInvalidInput
	}
	return nil
}

func bootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, email, password string, onlyIfNeeded bool, requestID string) (PublicUser, bool, error) {
	if err := ValidateAdminCredentials(email, password); err != nil {
		return PublicUser{}, false, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return PublicUser{}, false, fmt.Errorf("hash admin password: %w", err)
	}
	userID, err := id.NewV7()
	if err != nil {
		return PublicUser{}, false, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return PublicUser{}, false, fmt.Errorf("begin admin bootstrap: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748629301)`); err != nil {
		return PublicUser{}, false, fmt.Errorf("lock initial administrator: %w", err)
	}
	if onlyIfNeeded {
		var configured bool
		if err := tx.QueryRow(ctx, adminInitializedQuery).Scan(&configured); err != nil {
			return PublicUser{}, false, fmt.Errorf("inspect administrator state: %w", err)
		}
		if configured {
			return PublicUser{}, false, nil
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES ($1,$2,$3,'active')`, userID, email, string(passwordHash)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return PublicUser{}, false, ErrAlreadyExists
		}
		return PublicUser{}, false, fmt.Errorf("insert admin user: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_code) VALUES ($1,'admin')`, userID); err != nil {
		return PublicUser{}, false, fmt.Errorf("assign admin role: %w", err)
	}
	public := PublicUser{ID: userID, Email: email, Status: "active", Timezone: "Asia/Shanghai", Roles: []string{"admin"}}
	auditID, err := id.NewV7()
	if err != nil {
		return PublicUser{}, false, err
	}
	after, err := json.Marshal(public)
	if err != nil {
		return PublicUser{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,'initialize','user',$2,$3,$4)`, auditID, userID, string(after), requestID); err != nil {
		return PublicUser{}, false, fmt.Errorf("audit administrator initialization: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO installation_setup(id,completed_at) VALUES (1,clock_timestamp())
ON CONFLICT(id) DO UPDATE SET completed_at=COALESCE(installation_setup.completed_at,EXCLUDED.completed_at)`); err != nil {
		return PublicUser{}, false, fmt.Errorf("complete administrator initialization: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicUser{}, false, fmt.Errorf("commit admin bootstrap: %w", err)
	}
	return public, true, nil
}
