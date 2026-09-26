package routing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRoutingOwnerLimitAndLineAuthorization(t *testing.T) {
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
	add := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	owner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'routing-owner@example.invalid','hash','active') RETURNING id::text`)
	other := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'routing-other@example.invalid','hash','active') RETURNING id::text`)
	group := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),'TEST.ROUTING','Routing','JP') RETURNING id::text`)
	node := add(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities) VALUES(gen_random_uuid(),$1,'Routing Node','JP','routing.example.invalid',443,ARRAY['proxy']) RETURNING id::text`, group)
	line := add(`INSERT INTO lines(id,name,created_by) VALUES(gen_random_uuid(),'Routing Line',$1) RETURNING id::text`, owner)
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'egress')`, line, node); err != nil {
		t.Fatal(err)
	}
	plan := add(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),'routing-plan',1000000) RETURNING id::text`)
	snapshot, _ := json.Marshal(map[string]any{"resource_group_ids": []string{group}, "line_ids": []string{line}, "limits": map[string]any{"max_routing_rules": 1}})
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, owner, plan, snapshot); err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool)
	profile, err := repo.CreateProfile(ctx, owner, ProfileInput{Name: "Main", FallbackKind: "line", FallbackLineID: &line, Enabled: true}, "test")
	if err != nil || profile.ID == "" {
		t.Fatalf("create profile: %+v %v", profile, err)
	}
	if _, err := repo.GetOwnProfile(ctx, other, profile.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign profile: %v", err)
	}
	rule, err := repo.CreateRule(ctx, owner, profile.ID, RuleInput{Priority: 10, MatchType: "domain", MatchValue: "example.com", Action: "line", LineID: &line, Enabled: true}, "test")
	if err != nil || rule.ID == "" {
		t.Fatalf("create rule: %+v %v", rule, err)
	}
	if _, err := repo.CreateRule(ctx, owner, profile.ID, RuleInput{Priority: 20, MatchType: "geoip", MatchValue: "cn", Action: "direct", Enabled: true}, "test"); !errors.Is(err, ErrLimit) {
		t.Fatalf("rule limit: %v", err)
	}
	if _, err := repo.CreateRule(ctx, other, profile.ID, RuleInput{Priority: 30, MatchType: "domain", MatchValue: "other.com", Action: "direct", Enabled: true}, "test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign rule: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json=jsonb_set(snapshot_json,'{line_ids}','[]') WHERE user_id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateProfile(ctx, owner, ProfileInput{Name: "Denied", FallbackKind: "line", FallbackLineID: &line, Enabled: true}, "test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked line: %v", err)
	}
	if err := repo.ValidateOwnProfile(ctx, owner, profile.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("validate revoked profile: %v", err)
	}
}
