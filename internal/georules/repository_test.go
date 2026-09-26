package georules

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestManagedRuleSetVersionActivation(t *testing.T) {
	url := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var actor string
	if err := pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'geo-actor@example.invalid','hash','active') RETURNING id::text`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_user_id=$1`, actor)
		_, _ = pool.Exec(ctx, `DELETE FROM routing_rule_sets WHERE code='ai' AND kind='geosite'`)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, actor)
	}()
	repo := NewPostgresRepository(pool)
	newInput := func(version, entry string) Input {
		t.Helper()
		in, err := Normalize(NewRuleSet{Kind: "geosite", Code: "ai", Name: "AI", Version: version, Source: "test", Entries: []string{entry}})
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	first, err := repo.Create(ctx, newInput("v1", "domain:openai.com"), actor, "test")
	if err != nil || !first.Enabled || first.EntryCount != 1 {
		t.Fatalf("first version: %+v %v", first, err)
	}
	second, err := repo.Create(ctx, newInput("v2", "suffix:anthropic.com"), actor, "test")
	if err != nil || !second.Enabled {
		t.Fatalf("second version: %+v %v", second, err)
	}
	active, err := LoadEnabled(ctx, pool, "geosite", "ai")
	if err != nil || active.Version != "v2" || len(active.Entries) != 1 || active.Entries[0] != "suffix:anthropic.com" {
		t.Fatalf("active after second upload: %+v %v", active, err)
	}
	if _, err := repo.SetEnabled(ctx, first.ID, true, actor, "test"); err != nil {
		t.Fatal(err)
	}
	active, err = LoadEnabled(ctx, pool, "geosite", "ai")
	if err != nil || active.Version != "v1" {
		t.Fatalf("active after rollback: %+v %v", active, err)
	}
	if _, err := repo.Create(ctx, newInput("v1", "domain:other.com"), actor, "test"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate version: %v", err)
	}
	if _, err := repo.SetEnabled(ctx, first.ID, false, actor, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEnabled(ctx, pool, "geosite", "ai"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled active version: %v", err)
	}
}
