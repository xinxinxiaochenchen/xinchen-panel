package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLoadMigrationsReadsOrderedFiles(t *testing.T) {
	migrations, err := LoadMigrations(fstest.MapFS{
		"000002_second.up.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
		"000001_first.up.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 || migrations[0].Version != 1 || migrations[1].Version != 2 {
		t.Fatalf("unexpected order: %+v", migrations)
	}
	if migrations[0].Checksum == "" || migrations[0].Checksum == migrations[1].Checksum {
		t.Fatalf("unexpected checksums: %+v", migrations)
	}
}

func TestLoadMigrationsRejectsDuplicateVersion(t *testing.T) {
	_, err := LoadMigrations(fstest.MapFS{
		"000001_first.up.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000001_second.up.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
	})
	if err == nil {
		t.Fatal("expected duplicate version error")
	}
}

func TestLoadMigrationsIgnoresAppleDoubleMetadata(t *testing.T) {
	migrations, err := LoadMigrations(fstest.MapFS{
		"000001_first.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"._000001_first.up.sql": &fstest.MapFile{Data: []byte("macOS metadata")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 || migrations[0].Version != 1 {
		t.Fatalf("unexpected migrations: %+v", migrations)
	}
}

func TestPendingMigrationsRejectsChangedAppliedFile(t *testing.T) {
	migrations, err := LoadMigrations(fstest.MapFS{
		"000001_first.up.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = PendingMigrations(migrations, map[int64]string{1: strings.Repeat("0", 64)})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestPendingMigrationsRejectsRetroactiveVersion(t *testing.T) {
	migrations, err := LoadMigrations(fstest.MapFS{
		"000001_first.up.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000002_second.up.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = PendingMigrations(migrations, map[int64]string{2: migrations[1].Checksum})
	if err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("expected retroactive migration error, got %v", err)
	}
}

func TestRepositoryMigrationLoads(t *testing.T) {
	migrations, err := LoadMigrations(os.DirFS("../../../migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 26 {
		t.Fatalf("expected Agent latency migration 26; found %d migrations", len(migrations))
	}
	for i, migration := range migrations {
		if migration.Version != int64(i+1) {
			t.Fatalf("expected contiguous migration version %d, got %d", i+1, migration.Version)
		}
	}
}

func TestProxyAccessMigrationGrantsUserPermissions(t *testing.T) {
	contents, err := os.ReadFile("../../../migrations/000008_proxy_accesses.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"proxy_accesses.read", "proxy_accesses.write"} {
		if !strings.Contains(string(contents), code) {
			t.Fatalf("migration 8 does not grant %s", code)
		}
	}
}

func TestMigration24ForwardRuleLineConstraint(t *testing.T) {
	dsn := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var applied bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=24)`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Skip("migration 24 is not applied to the configured test database")
	}
	suffix := fmt.Sprintf("migration24-%d", time.Now().UnixNano())
	insertID := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	owner := insertID(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, suffix+"@example.invalid")
	group := insertID(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,$1,'US') RETURNING id::text`, "MIG24."+suffix)
	ingress := insertID(`INSERT INTO nodes(id,group_id,name,region,host,capabilities) VALUES(gen_random_uuid(),$1,$2,'US',$3,ARRAY['forward']) RETURNING id::text`, group, suffix, suffix+".example.invalid")
	line := insertID(`INSERT INTO lines(id,name,created_by) VALUES(gen_random_uuid(),$1,$2) RETURNING id::text`, suffix, owner)
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM forward_rules WHERE name LIKE $1`, suffix+"%")
		_, _ = pool.Exec(cleanup, `DELETE FROM lines WHERE id=$1`, line)
		_, _ = pool.Exec(cleanup, `DELETE FROM nodes WHERE id=$1`, ingress)
		_, _ = pool.Exec(cleanup, `DELETE FROM resource_groups WHERE id=$1`, group)
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id=$1`, owner)
	})
	insertRule := func(name string, targetNode any, targetHost any, lineID any) error {
		_, err := pool.Exec(ctx, `INSERT INTO forward_rules(id,user_id,name,ingress_node_id,ingress_port,target_node_id,target_host,target_port,line_id,protocol)
VALUES(gen_random_uuid(),$1,$2,$3,25124,$4,$5,443,$6,'TCP')`, owner, name, ingress, targetNode, targetHost, lineID)
		return err
	}
	if err := insertRule(suffix+"-up-line", nil, "example.org", line); err != nil {
		t.Fatalf("migration 24 rejected line-only rule: %v", err)
	}
	if err := insertRule(suffix+"-up-both", ingress, nil, line); err == nil || !strings.Contains(err.Error(), "forward_rules_line_target_check") {
		t.Fatalf("migration 24 accepted both targets or returned wrong error: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM forward_rules WHERE user_id=$1`, owner); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../../migrations/000024_forward_rule_lines.down.sql")
	if err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(down), pgx.QueryExecModeSimpleProtocol); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO forward_rules(id,user_id,name,ingress_node_id,ingress_port,target_host,target_port,protocol)
VALUES(gen_random_uuid(),$1,$2,$3,25125,'example.org',443,'TCP')`, owner, suffix+"-down-direct", ingress); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("migration 24 down rejected direct-only rule: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO forward_rules(id,user_id,name,ingress_node_id,ingress_port,target_host,target_port,line_id,protocol)
VALUES(gen_random_uuid(),$1,$2,$3,25126,'example.org',443,$4,'TCP')`, owner, suffix+"-down-line", ingress, line); err == nil || !strings.Contains(err.Error(), "forward_rules_direct_only") {
		tx.Rollback(ctx)
		t.Fatalf("migration 24 down accepted line-bound rule or returned wrong error: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}
