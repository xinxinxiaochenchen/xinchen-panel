package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"controlplane/internal/agentidentity"
	"controlplane/internal/platform/db"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := LoadTokenConfig(os.LookupEnv, os.Args[1:])
	if err != nil {
		logger.Error("invalid Agent token configuration", "error", err)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	var actorID string
	var authorized bool
	err = pool.QueryRow(ctx, `SELECT u.id::text,EXISTS(SELECT 1 FROM user_roles WHERE user_id=u.id AND role_code='admin')
FROM users u WHERE lower(u.email)=$1 AND u.status='active'`, cfg.AdminEmail).Scan(&actorID, &authorized)
	if err != nil || !authorized {
		logger.Error("active administrator not found")
		os.Exit(1)
	}
	output, err := ReserveTokenOutput(cfg.OutputFile)
	if err != nil {
		logger.Error("reserve Agent token file failed", "error", err)
		os.Exit(1)
	}
	result, err := agentidentity.NewEnrollmentService(pool, nil).CreateToken(ctx, cfg.NodeID, actorID, "local-agent-token-cli")
	if err != nil {
		output.Close()
		logger.Error("Agent token issue failed", "error", err)
		os.Exit(1)
	}
	if err := output.Write(result); err != nil {
		output.Close()
		logger.Error("write Agent token file failed", "error", err)
		os.Exit(1)
	}
	if err := output.Close(); err != nil {
		logger.Error("close Agent token file failed", "error", err)
		os.Exit(1)
	}
	logger.Info("Agent token saved", "path", cfg.OutputFile, "expires_at", result.ExpiresAt)
}
