package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"controlplane/internal/catalog"
	"controlplane/internal/platform/db"
	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

func main() {
	if err := run(); err != nil {
		slog.Error("node bootstrap failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := LoadBootstrapConfig(os.LookupEnv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	var actorID string
	err = pool.QueryRow(ctx, `SELECT u.id::text FROM users u WHERE lower(u.email)=$1 AND u.status='active'
AND EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id=u.id AND ur.role_code='admin')`, cfg.AdminEmail).Scan(&actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("active administrator not found")
	}
	if err != nil {
		return fmt.Errorf("find administrator: %w", err)
	}
	repository := catalog.NewPostgresRepository(pool)
	group, err := catalog.NormalizeGroup(catalog.NewGroup{Code: cfg.GroupCode, Name: cfg.GroupName, Region: cfg.GroupRegion})
	if err != nil {
		return err
	}
	var groupID string
	var groupEnabled bool
	err = pool.QueryRow(ctx, `SELECT id::text,enabled FROM resource_groups WHERE code=$1`, group.Code).Scan(&groupID, &groupEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		groupID, err = id.NewV7()
		if err != nil {
			return fmt.Errorf("generate resource group ID: %w", err)
		}
		groupEnabled = group.Enabled
	} else if err != nil {
		return fmt.Errorf("find resource group: %w", err)
	}
	if !groupEnabled {
		return errors.New("resource group is disabled")
	}
	var publicIP *string
	if cfg.PublicIP != "" {
		publicIP = &cfg.PublicIP
	}
	node, err := catalog.NormalizeNode(catalog.NewNode{GroupID: groupID, Name: cfg.NodeName, Region: cfg.NodeRegion,
		Host: cfg.NodeHost, PublicIP: publicIP, ProxyPort: cfg.ProxyPort, RelayPort: cfg.RelayPort, Capabilities: cfg.Capabilities,
		Enabled: boolPointer(true)})
	if err != nil {
		return err
	}
	requestID, err := id.NewV7()
	if err != nil {
		return fmt.Errorf("generate bootstrap request ID: %w", err)
	}
	_, created, inserted, err := repository.EnsureGroupAndNode(ctx, groupID, group, node, actorID, requestID)
	if err != nil {
		return fmt.Errorf("ensure node: %w", err)
	}
	fmt.Printf("node_id=%s group_id=%s capabilities=%s created=%t\n", created.ID, created.GroupID, strings.Join(created.Capabilities, ","), inserted)
	return nil
}

func boolPointer(value bool) *bool { return &value }
