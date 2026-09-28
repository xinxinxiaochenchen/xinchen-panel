package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestMigration28RemovesRoutingAndPreservesSubscriptions(t *testing.T) {
	dsn := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(sql string) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatal(err)
		}
	}
	schema := pgx.Identifier{fmt.Sprintf("routing_upgrade_%d", time.Now().UnixNano())}.Sanitize()
	exec("CREATE SCHEMA " + schema)
	exec("SET LOCAL search_path = " + schema + ", public")
	migrations, err := LoadMigrations(os.DirFS("../../../migrations"))
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.Version < 28 {
			exec(migration.SQL)
		}
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	exec(read("testdata/routing_upgrade.sql"))
	preserved := func() string {
		t.Helper()
		var state string
		err := tx.QueryRow(ctx, `SELECT jsonb_build_object(
'subscriptions',(SELECT jsonb_agg(to_jsonb(s)-'routing_profile_id' ORDER BY id) FROM subscriptions s),
'targets',(SELECT jsonb_agg(to_jsonb(t) ORDER BY subscription_id) FROM subscription_proxy_targets t),
'accesses',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM proxy_accesses a),
'candidates',(SELECT jsonb_agg(to_jsonb(c) ORDER BY proxy_access_id,position) FROM proxy_access_lines c),
'plans',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM plans p),
'limits',(SELECT jsonb_agg(to_jsonb(l)-'max_routing_rules' ORDER BY plan_id) FROM plan_limits l),
'memberships',(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM memberships m),
'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM lines l),
'nodes',(SELECT jsonb_agg(to_jsonb(n) ORDER BY id) FROM nodes n))::text`).Scan(&state)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := preserved()
	up := read("../../../migrations/000028_remove_routing.up.sql")
	exec(up)
	if after := preserved(); after != before {
		t.Fatalf("upgrade changed subscriptions or other business data:\nbefore %s\nafter %s", before, after)
	}
	assertCount := func(query string, want int) {
		t.Helper()
		var got int
		if err := tx.QueryRow(ctx, query).Scan(&got); err != nil || got != want {
			t.Fatalf("query %s = %d, want %d: %v", query, got, want, err)
		}
	}
	assertRemoved := func() {
		t.Helper()
		assertCount(`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname IN ('routing_profiles','routing_rules','routing_rule_sets','subscriptions_routing_profile_idx')`, 0)
		assertCount(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND ((table_name='subscriptions' AND column_name='routing_profile_id') OR (table_name='plan_limits' AND column_name='max_routing_rules'))`, 0)
		assertCount(`SELECT count(*) FROM permissions WHERE code IN ('routing.read','routing.write','routing_rulesets.read','routing_rulesets.write')`, 0)
		assertCount(`SELECT count(*) FROM role_permissions WHERE permission_code IN ('routing.read','routing.write','routing_rulesets.read','routing_rulesets.write')`, 0)
		assertCount(`SELECT count(*) FROM role_permissions WHERE role_code='upgrade-custom' AND permission_code='subscriptions.read'`, 1)
	}
	assertRemoved()
	// Downgrade restores empty structures; dropped policy data requires a backup.
	exec(read("../../../migrations/000028_remove_routing.down.sql"))
	assertCount(`SELECT count(*) FROM routing_profiles`, 0)
	assertCount(`SELECT count(*) FROM routing_rules`, 0)
	assertCount(`SELECT count(*) FROM routing_rule_sets`, 0)
	assertCount(`SELECT count(*) FROM subscriptions WHERE routing_profile_id IS NULL`, 2)
	assertCount(`SELECT count(*) FROM plan_limits WHERE max_routing_rules=0 AND max_subscriptions=3`, 1)
	assertCount(`SELECT count(*) FROM role_permissions WHERE (role_code IN ('admin','user') AND permission_code IN ('routing.read','routing.write')) OR (role_code='admin' AND permission_code IN ('routing_rulesets.read','routing_rulesets.write'))`, 6)
	exec(up)
	assertRemoved()
	if preserved() != before {
		t.Fatal("up/down/up changed preserved business data")
	}
}
