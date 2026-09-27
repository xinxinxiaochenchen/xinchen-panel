package entitlement

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresPlanGrantsSharedMultihopLine(t *testing.T) {
	databaseURL := entitlementTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	add := func(query string, args ...any) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	actor := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "grant-actor-"+suffix+"@example.invalid")
	hk := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,'Hong Kong','HK') RETURNING id::text`, "GRANT.HK."+suffix)
	jp := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,'Japan','JP') RETURNING id::text`, "GRANT.JP."+suffix)
	ingress := add(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Ingress','HK',$2,443,24101,ARRAY['proxy','forward']) RETURNING id::text`, hk, "grant-ingress-"+suffix+".example.invalid")
	egress := add(`INSERT INTO nodes(id,group_id,name,region,host,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Egress','JP',$2,24102,ARRAY['forward']) RETURNING id::text`, jp, "grant-egress-"+suffix+".example.invalid")
	line := add(`INSERT INTO lines(id,name,created_by,relay_generation) VALUES(gen_random_uuid(),$1,$2,1) RETURNING id::text`, "Shared grant "+suffix, actor)
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'ingress'),($1,1,$3,'egress')`, line, ingress, egress); err != nil {
		t.Fatal(err)
	}
	var createdPlan string
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM audit_logs WHERE actor_user_id=$1`, actor)
		if createdPlan != "" {
			_, _ = pool.Exec(cleanup, `DELETE FROM plans WHERE id=$1`, createdPlan)
		}
		_, _ = pool.Exec(cleanup, `DELETE FROM lines WHERE id=$1`, line)
		_, _ = pool.Exec(cleanup, `DELETE FROM nodes WHERE id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(cleanup, `DELETE FROM resource_groups WHERE id=ANY($1::uuid[])`, []string{hk, jp})
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id=$1`, actor)
	})
	repo := NewPostgresRepository(pool)
	input := PlanInput{Name: "Shared multi " + suffix, QuotaBytes: 1000000, DefaultMultiplierMilli: 1000,
		Limits: PlanLimits{MaxHops: 2}, ResourceGroupIDs: []string{hk, jp}, LineIDs: []string{line}}
	plan, err := repo.CreatePlan(ctx, input, actor, "grant-multihop")
	if err != nil || len(plan.LineIDs) != 1 || plan.LineIDs[0] != line {
		t.Fatalf("multihop grant = %+v, %v", plan, err)
	}
	createdPlan = plan.ID
	input.Name = "Missing group " + suffix
	input.ResourceGroupIDs = []string{hk}
	if _, err := repo.CreatePlan(ctx, input, actor, "missing-group"); err == nil {
		t.Fatal("granted shared line with ungranted egress group")
	}
	input.Name = "Too few hops " + suffix
	input.ResourceGroupIDs = []string{hk, jp}
	input.Limits.MaxHops = 1
	if _, err := repo.CreatePlan(ctx, input, actor, "too-few-hops"); err == nil {
		t.Fatal("granted shared line above plan hop limit")
	}
	input.Name = "Disabled line " + suffix
	input.Limits.MaxHops = 2
	if _, err := pool.Exec(ctx, `UPDATE lines SET enabled=false WHERE id=$1`, line); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreatePlan(ctx, input, actor, "disabled-line"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled shared line grant = %v", err)
	}
}
