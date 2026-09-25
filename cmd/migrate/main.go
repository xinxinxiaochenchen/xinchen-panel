package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"controlplane/internal/platform/config"
	"controlplane/internal/platform/db"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "up" {
		slog.Error("usage: migrate up")
		os.Exit(2)
	}
	cfg, err := config.LoadFrom(os.LookupEnv)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := db.RunMigrations(ctx, pool, os.DirFS("migrations")); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations applied")
}
