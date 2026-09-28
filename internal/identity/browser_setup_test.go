package identity

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"controlplane/internal/platform/db"
	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testDatabaseURL()
	if dsn == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := id.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{"setup_test_" + strings.ReplaceAll(uid, "-", "")}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	if err := db.RunMigrations(ctx, pool, os.DirFS("../../migrations")); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestBrowserSetupConcurrentCreationAndPermanentClosure(t *testing.T) {
	pool := setupTestPool(t)
	ctx := context.Background()
	store := NewSetupRepository(pool)
	if required, err := store.Required(ctx); err != nil || !required {
		t.Fatalf("fresh required=%t err=%v", required, err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, created, err := store.Create(ctx, "owner@example.test", "long-setup-password", "setup-request")
			if err != nil {
				t.Error(err)
			}
			results <- created
		}()
	}
	wg.Wait()
	close(results)
	created := 0
	for result := range results {
		if result {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("initial administrators created=%d", created)
	}
	var users, audits int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE action='initialize' AND request_id='setup-request'").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if users != 1 || audits != 1 {
		t.Fatalf("users=%d audits=%d", users, audits)
	}
	for _, sql := range []string{"UPDATE users SET status='disabled'", "DELETE FROM audit_logs; DELETE FROM user_roles; DELETE FROM users"} {
		if _, err := pool.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatal(err)
		}
		if required, err := store.Required(ctx); err != nil || required {
			t.Fatalf("setup reopened: required=%t err=%v", required, err)
		}
		if _, created, err := store.Create(ctx, "other@example.test", "other-long-password", "retry"); err != nil || created {
			t.Fatalf("setup repeated: created=%t err=%v", created, err)
		}
	}
}

func TestBrowserSetupAuditFailureRollsBackAdminAndCompletion(t *testing.T) {
	pool := setupTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "ALTER TABLE audit_logs ADD CONSTRAINT reject_setup CHECK(action <> 'initialize')"); err != nil {
		t.Fatal(err)
	}
	store := NewSetupRepository(pool)
	if _, created, err := store.Create(ctx, "owner@example.test", "long-setup-password", "setup-request"); err == nil || created {
		t.Fatalf("failed audit created=%t err=%v", created, err)
	}
	if required, err := store.Required(ctx); err != nil || !required {
		t.Fatalf("failed setup completed: required=%t err=%v", required, err)
	}
	var users int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); err != nil || users != 0 {
		t.Fatalf("users=%d err=%v", users, err)
	}
}
