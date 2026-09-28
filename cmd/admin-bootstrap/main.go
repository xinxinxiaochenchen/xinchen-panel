package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"os"
	"strings"
	"time"

	"controlplane/internal/identity"
	"controlplane/internal/platform/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("admin bootstrap failed", "error", err)
		os.Exit(1)
	}
}

type bootstrapStore interface {
	HasAdmin(context.Context) (bool, error)
	Create(context.Context, string, string, bool) (identity.PublicUser, bool, error)
}

type postgresBootstrapStore struct{ pool *pgxpool.Pool }

func (s postgresBootstrapStore) HasAdmin(ctx context.Context) (bool, error) {
	return identity.AdminConfigured(ctx, s.pool)
}
func (s postgresBootstrapStore) Create(ctx context.Context, email, password string, onlyIfNeeded bool) (identity.PublicUser, bool, error) {
	if onlyIfNeeded {
		return identity.BootstrapAdminIfNeeded(ctx, s.pool, email, password)
	}
	user, err := identity.BootstrapAdmin(ctx, s.pool, email, password)
	return user, err == nil, err
}

func bootstrapAction(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	if len(args) == 1 && (args[0] == "--status" || args[0] == "--if-needed") {
		return args[0], nil
	}
	return "", fmt.Errorf("usage: admin-bootstrap [--status | --if-needed]")
}

func run() error {
	action, err := bootstrapAction(os.Args[1:])
	if err != nil {
		return err
	}
	databaseURL := os.Getenv("CONTROL_DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("CONTROL_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return executeBootstrap(ctx, action, os.Getenv("CONTROL_ADMIN_EMAIL"), os.Stdin, os.Stdout, postgresBootstrapStore{pool: pool})
}

func executeBootstrap(ctx context.Context, action, email string, input io.Reader, output io.Writer, store bootstrapStore) error {
	if action == "--status" || action == "--if-needed" {
		configured, err := store.HasAdmin(ctx)
		if err != nil {
			return fmt.Errorf("inspect administrator state: %w", err)
		}
		if action == "--status" {
			value := "empty"
			if configured {
				value = "configured"
			}
			_, err := fmt.Fprintln(output, value)
			return err
		}
		if configured {
			_, err := fmt.Fprintln(output, "Administrator already configured.")
			return err
		}
	}
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return fmt.Errorf("CONTROL_ADMIN_EMAIL must be a valid email address")
	}
	if file, ok := input.(*os.File); ok {
		stat, err := file.Stat()
		if err != nil {
			return fmt.Errorf("inspect password input: %w", err)
		}
		if stat.Mode()&os.ModeCharDevice != 0 {
			return fmt.Errorf("password must be piped on stdin so it is not echoed")
		}
	}
	data, err := io.ReadAll(io.LimitReader(input, 75))
	if err != nil {
		return fmt.Errorf("read password from stdin: %w", err)
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if len(password) < 12 || len(password) > 72 || strings.ContainsAny(password, "\r\n") {
		return fmt.Errorf("password must contain 12–72 bytes on one line")
	}
	admin, created, err := store.Create(ctx, email, password, action == "--if-needed")
	if err != nil {
		return err
	}
	if !created {
		_, err = fmt.Fprintln(output, "Administrator already configured.")
	} else {
		_, err = fmt.Fprintf(output, "Created administrator %s (%s)\n", admin.Email, admin.ID)
	}
	return err
}
