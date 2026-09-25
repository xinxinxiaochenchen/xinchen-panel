package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var migrationFilename = regexp.MustCompile(`^([0-9]+)_[a-z0-9_-]+\.up\.sql$`)

type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

func LoadMigrations(files fs.FS) ([]Migration, error) {
	names, err := fs.Glob(files, "*.up.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	if len(names) == 0 {
		return nil, errors.New("no up migrations found")
	}
	migrations := make([]Migration, 0, len(names))
	seen := make(map[int64]struct{}, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, "._") {
			continue
		}
		parts := migrationFilename.FindStringSubmatch(name)
		if parts == nil {
			return nil, fmt.Errorf("invalid migration filename %q", name)
		}
		version, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid migration version in %q", name)
		}
		if _, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d", version)
		}
		seen[version] = struct{}{}
		content, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		sum := sha256.Sum256(content)
		migrations = append(migrations, Migration{Version: version, Name: name, SQL: string(content), Checksum: hex.EncodeToString(sum[:])})
	}
	if len(migrations) == 0 {
		return nil, errors.New("no up migrations found")
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

func PendingMigrations(migrations []Migration, applied map[int64]string) ([]Migration, error) {
	known := make(map[int64]Migration, len(migrations))
	for _, migration := range migrations {
		known[migration.Version] = migration
	}
	var maxApplied int64
	for version, checksum := range applied {
		migration, ok := known[version]
		if !ok {
			return nil, fmt.Errorf("applied migration %d has no file", version)
		}
		if migration.Checksum != checksum {
			return nil, fmt.Errorf("migration %d checksum mismatch", version)
		}
		if version > maxApplied {
			maxApplied = version
		}
	}
	var pending []Migration
	for _, migration := range migrations {
		if _, ok := applied[migration.Version]; ok {
			continue
		}
		if migration.Version < maxApplied {
			return nil, fmt.Errorf("unapplied migration %d is older than applied migration %d", migration.Version, maxApplied)
		}
		pending = append(pending, migration)
	}
	return pending, nil
}

const migrationLockKey int64 = 0x4e43504c414e45

func RunMigrations(ctx context.Context, pool *pgxpool.Pool, files fs.FS) error {
	migrations, err := LoadMigrations(files)
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationLockKey); err != nil {
			_ = conn.Conn().Close(context.Background())
		}
	}()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version bigint PRIMARY KEY,
		checksum text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	rows, err := conn.Query(ctx, "SELECT version, checksum FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	applied := make(map[int64]string)
	for rows.Next() {
		var version int64
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate applied migrations: %w", err)
	}
	rows.Close()
	pending, err := PendingMigrations(migrations, applied)
	if err != nil {
		return err
	}
	for _, migration := range pending {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", migration.Version, err)
		}
		if _, err := tx.Exec(ctx, migration.SQL, pgx.QueryExecModeSimpleProtocol); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %d: %w", migration.Version, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version, checksum) VALUES($1, $2)", migration.Version, migration.Checksum); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %d: %w", migration.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %d: %w", migration.Version, err)
		}
	}
	return nil
}
