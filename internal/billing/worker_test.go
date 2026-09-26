package billing

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresPeriodWorkerRenewsSnapshotOnce(t *testing.T) {
	dsn := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	add := func(q string, args ...any) string {
		t.Helper()
		var v string
		if e := pool.QueryRow(ctx, q, args...).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	owner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'worker-owner@example.invalid','hash','active') RETURNING id::text`)
	plan := add(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),'worker-plan',1000) RETURNING id::text`)
	exec(`INSERT INTO plan_limits(plan_id) VALUES($1)`, plan)
	start := time.Now().UTC().AddDate(0, -2, 0)
	start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	snapshot, _ := json.Marshal(map[string]any{"plan_name": "Worker Plan", "quota_bytes": 1000, "default_multiplier_milli": 1000, "resource_group_ids": []string{}, "line_ids": []string{}, "limits": map[string]int{"max_hops": 1}})
	member := add(`INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,$3,$4,'active',1,'UTC',$5) RETURNING id::text`, owner, plan, start, time.Now().Add(24*time.Hour), snapshot)
	repo := NewPostgresRepository(pool)
	exec(`UPDATE plans SET quota_bytes=2000,default_multiplier_milli=500 WHERE id=$1`, plan)
	worker := NewPeriodWorker(repo)
	count, err := worker.Sweep(ctx)
	if err != nil || count != 1 {
		t.Fatalf("renewal sweep: %d %v", count, err)
	}
	current, err := repo.EnsurePeriod(ctx, member, time.Now())
	if err != nil || current.QuotaBytes != 2000 {
		t.Fatalf("renewed period: %+v %v", current, err)
	}
	first, err := repo.EnsurePeriod(ctx, member, start.Add(time.Hour))
	if err != nil || first.ID == current.ID || first.QuotaBytes != 1000 {
		t.Fatalf("late historical period: %+v %v", first, err)
	}
	var frozen, renewed []byte
	if err := pool.QueryRow(ctx, `SELECT snapshot_json FROM billing_periods WHERE id=$1`, first.ID).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT snapshot_json FROM memberships WHERE id=$1`, member).Scan(&renewed); err != nil {
		t.Fatal(err)
	}
	var firstSnapshot, nextSnapshot struct {
		QuotaBytes             int64 `json:"quota_bytes"`
		DefaultMultiplierMilli int   `json:"default_multiplier_milli"`
	}
	if err := json.Unmarshal(frozen, &firstSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(renewed, &nextSnapshot); err != nil {
		t.Fatal(err)
	}
	if firstSnapshot.QuotaBytes != 1000 || nextSnapshot.QuotaBytes != 2000 || nextSnapshot.DefaultMultiplierMilli != 500 {
		t.Fatalf("snapshots changed incorrectly: %+v %+v", firstSnapshot, nextSnapshot)
	}
	count, err = worker.Sweep(ctx)
	if err != nil || count != 0 {
		t.Fatalf("duplicate renewal: %d %v", count, err)
	}
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE kind='billing.period_renewed' AND aggregate_id=$1`, member).Scan(&events); err != nil || events != 1 {
		t.Fatalf("renewal outbox: %d %v", events, err)
	}
	exec(`UPDATE users SET status='disabled' WHERE id=$1`, owner)
	count, err = worker.Sweep(ctx)
	if err != nil || count != 0 {
		t.Fatalf("disabled owner renewal: %d %v", count, err)
	}
}
