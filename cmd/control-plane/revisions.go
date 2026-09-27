package main

import (
	"controlplane/internal/orchestration"
	"controlplane/internal/platform/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newRevisionRepository(pool *pgxpool.Pool, cfg config.Config) (*orchestration.RevisionRepository, error) {
	if len(cfg.RelaySecretKey) == 0 {
		return orchestration.NewRevisionRepository(pool), nil
	}
	store, err := orchestration.NewPostgresRelaySecretStore(pool, cfg.RelaySecretKey)
	if err != nil {
		return nil, err
	}
	return orchestration.NewRevisionRepositoryWithRelaySecrets(pool, store), nil
}
