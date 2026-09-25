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

func BootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, email, password string) (PublicUser, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(password) < 12 || len([]byte(password)) > 72 {
		return PublicUser{}, ErrInvalidInput
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return PublicUser{}, fmt.Errorf("hash admin password: %w", err)
	}
	userID, err := id.NewV7()
	if err != nil {
		return PublicUser{}, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return PublicUser{}, fmt.Errorf("begin admin bootstrap: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES ($1,$2,$3,'active')`, userID, email, string(passwordHash)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return PublicUser{}, ErrAlreadyExists
		}
		return PublicUser{}, fmt.Errorf("insert admin user: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_code) VALUES ($1,'admin')`, userID); err != nil {
		return PublicUser{}, fmt.Errorf("assign admin role: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicUser{}, fmt.Errorf("commit admin bootstrap: %w", err)
	}
	return PublicUser{ID: userID, Email: email, Status: "active", Timezone: "Asia/Shanghai", Roles: []string{"admin"}}, nil
}
