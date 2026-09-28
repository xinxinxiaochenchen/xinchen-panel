package identity

import (
	"context"
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

// AdminConfigured reports whether an active system administrator exists.
func AdminConfigured(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var configured bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (
        SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id
        WHERE u.status='active' AND ur.role_code='admin')`).Scan(&configured)
	return configured, err
}

func BootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, email, password string) (PublicUser, error) {
	user, _, err := bootstrapAdmin(ctx, pool, email, password, false)
	return user, err
}

// BootstrapAdminIfNeeded creates only the first active administrator. Repeated
// or concurrent installer runs never reset an existing administrator password.
func BootstrapAdminIfNeeded(ctx context.Context, pool *pgxpool.Pool, email, password string) (PublicUser, bool, error) {
	return bootstrapAdmin(ctx, pool, email, password, true)
}

func bootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, email, password string, onlyIfNeeded bool) (PublicUser, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(password) < 12 || len([]byte(password)) > 72 {
		return PublicUser{}, false, ErrInvalidInput
	}
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
	if onlyIfNeeded {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(748629301)`); err != nil {
			return PublicUser{}, false, fmt.Errorf("lock initial administrator: %w", err)
		}
		var configured bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
            SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id
            WHERE u.status='active' AND ur.role_code='admin')`).Scan(&configured); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
		return PublicUser{}, false, fmt.Errorf("commit admin bootstrap: %w", err)
	}
	return PublicUser{ID: userID, Email: email, Status: "active", Timezone: "Asia/Shanghai", Roles: []string{"admin"}}, true, nil
}
