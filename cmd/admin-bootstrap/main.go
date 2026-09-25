package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"controlplane/internal/identity"
	"controlplane/internal/platform/db"
)

func main() {
	if err := run(); err != nil {
		slog.Error("admin bootstrap failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("CONTROL_DATABASE_URL")
	email := os.Getenv("CONTROL_ADMIN_EMAIL")
	if databaseURL == "" || email == "" {
		return fmt.Errorf("CONTROL_DATABASE_URL and CONTROL_ADMIN_EMAIL are required")
	}
	stat, err := os.Stdin.Stat()
	if err != nil {
		return fmt.Errorf("inspect password input: %w", err)
	}
	if stat.Mode()&os.ModeCharDevice != 0 {
		return fmt.Errorf("password must be piped on stdin so it is not echoed")
	}
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return fmt.Errorf("read password from stdin: %w", err)
	}
	password = strings.TrimSuffix(password, "\n")
	password = strings.TrimSuffix(password, "\r")
	pool, err := db.Open(context.Background(), databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	admin, err := identity.BootstrapAdmin(context.Background(), pool, email, password)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Created administrator %s (%s)\n", admin.Email, admin.ID)
	return nil
}
