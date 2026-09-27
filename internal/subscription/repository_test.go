package subscription

import (
	"context"
	"controlplane/internal/proxyaccess"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestPostgresSubscriptionLifecycle(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	add := func(query string, args ...any) string {
		t.Helper()
		var v string
		if err := pool.QueryRow(ctx, query, args...).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	owner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'sub-owner@example.invalid','hash','active') RETURNING id::text`)
	other := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'sub-other@example.invalid','hash','active') RETURNING id::text`)
	group := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),'TEST.SUB','Subscriptions','JP') RETURNING id::text`)
	node := add(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities) VALUES(gen_random_uuid(),$1,'Japan','JP','jp.example.invalid',443,ARRAY['proxy']) RETURNING id::text`, group)
	line := add(`INSERT INTO lines(id,name,created_by) VALUES(gen_random_uuid(),'Japan',$1) RETURNING id::text`, owner)
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'egress')`, line, node); err != nil {
		t.Fatal(err)
	}
	plan := add(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),'sub-plan',1000000) RETURNING id::text`)
	snapshot, _ := json.Marshal(map[string]any{"resource_group_ids": []string{group}, "line_ids": []string{line}, "limits": map[string]any{"max_subscriptions": 1}})
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, owner, plan, snapshot); err != nil {
		t.Fatal(err)
	}
	cipher, _ := proxyaccess.NewCredentialCipher(make([]byte, 32))
	proxies := proxyaccess.NewPostgresRepository(pool, cipher)
	access, password, err := proxies.Create(ctx, owner, proxyaccess.AccessInput{Name: "Japan", LineID: line, Enabled: true}, "test")
	if err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool, cipher)
	input := Input{Name: "Laptop", NameTemplate: "{region} · {name} · {line}", ProxyAccessIDs: []string{access.ID}, Enabled: true}
	if _, _, err := repo.Create(ctx, other, input, "test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner create: %v", err)
	}
	sub, token, err := repo.Create(ctx, owner, input, "test")
	if err != nil || len(token) != 43 || len(sub.ProxyAccessIDs) != 1 {
		t.Fatalf("create: %+v %v", sub, err)
	}
	if _, _, err := repo.Create(ctx, owner, Input{Name: "Second", NameTemplate: input.NameTemplate, ProxyAccessIDs: input.ProxyAccessIDs, Enabled: true}, "test"); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := repo.GetOwn(ctx, other, sub.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner get: %v", err)
	}
	if got, err := repo.RevealOwn(ctx, strings.ToUpper(owner), strings.ToUpper(sub.ID)); err != nil || got != token {
		t.Fatalf("reveal: %v", err)
	}
	if got, err := repo.ListOwn(ctx, owner, 10, ""); err != nil || len(got) != 1 {
		t.Fatalf("list: %+v %v", got, err)
	}
	if got, err := repo.ResolveToken(ctx, token); err != nil || got.ID != sub.ID {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	if _, _, err := repo.Export(ctx, token, "mihomo"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("offline export: %v", err)
	}
	agent := add(`INSERT INTO agents(id,node_id,status,version,last_seen_at,desired_revision,applied_revision) VALUES(gen_random_uuid(),$1,'online','test',now(),1,1) RETURNING id::text`, node)
	_ = agent
	payload, _ := json.Marshal(map[string]any{"forward_config": []any{}, "proxy_config": []any{map[string]string{"id": access.ID, "credential_hash": proxyaccess.TrojanDigest(password)}}})
	if _, err := pool.Exec(ctx, `INSERT INTO config_revisions(node_id,revision,sha256,payload_json,status,applied_at) VALUES($1,1,$2,$3,'applied',now())`, node, strings.Repeat("a", 64), payload); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE proxy_accesses SET apply_status='active' WHERE id=$1`, access.ID); err != nil {
		t.Fatal(err)
	}
	if body, _, err := repo.Export(ctx, token, "mihomo"); err != nil || !strings.Contains(string(body), password) {
		t.Fatalf("ready export: %v %s", err, body)
	}
	fallbackLine := add(`INSERT INTO lines(id,name,created_by) VALUES(gen_random_uuid(),'Subscription fallback',$1) RETURNING id::text`, owner)
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'egress')`, fallbackLine, node); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO proxy_access_lines(proxy_access_id,line_id,position,priority,weight) VALUES($1,$2,1,20,3)`, access.ID, fallbackLine); err != nil {
		t.Fatal(err)
	}
	fallbackGrant, _ := json.Marshal(map[string]any{"resource_group_ids": []string{group}, "line_ids": []string{fallbackLine}, "limits": map[string]any{"max_subscriptions": 1, "max_hops": 1}})
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json=$2 WHERE user_id=$1`, owner, fallbackGrant); err != nil {
		t.Fatal(err)
	}
	fallbackPayload, _ := json.Marshal(map[string]any{"proxy_config": []any{map[string]any{
		"id": access.ID, "credential_hash": proxyaccess.TrojanDigest(password),
		"candidates": []any{map[string]any{"line_id": fallbackLine, "priority": 20, "weight": 3}},
	}}})
	if _, err := pool.Exec(ctx, `UPDATE config_revisions SET payload_json=$2 WHERE node_id=$1 AND revision=1`, node, fallbackPayload); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lines SET enabled=false WHERE id=$1`, line); err != nil {
		t.Fatal(err)
	}
	if body, _, err := repo.Export(ctx, token, "mihomo"); err != nil || strings.Count(string(body), "type: trojan") != 1 || !strings.Contains(string(body), "自动线路") {
		t.Fatalf("automatic fallback export: %v %s", err, body)
	}
	fallbackIDs := []string{access.ID}
	if _, err := repo.UpdateOwn(ctx, owner, sub.ID, Patch{ProxyAccessIDs: &fallbackIDs}, "fallback-target"); err != nil {
		t.Fatalf("subscription target cannot use authorized fallback: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lines SET enabled=true WHERE id=$1`, line); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM proxy_access_lines WHERE proxy_access_id=$1 AND line_id=$2`, access.ID, fallbackLine); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json=$2 WHERE user_id=$1`, owner, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE config_revisions SET payload_json=$2 WHERE node_id=$1 AND revision=1`, node, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM lines WHERE id=$1`, fallbackLine); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ExportOwn(ctx, other, sub.ID, "mihomo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner preview: %v", err)
	}
	if body, _, err := repo.ExportOwn(ctx, owner, sub.ID, "sing-box"); err != nil || !strings.Contains(string(body), password) {
		t.Fatalf("owner preview: %v", err)
	}
	profile := add(`INSERT INTO routing_profiles(id,user_id,name,fallback_kind,fallback_line_id) VALUES(gen_random_uuid(),$1,'Subscription policy','line',$2) RETURNING id::text`, owner, line)
	foreignProfile := add(`INSERT INTO routing_profiles(id,user_id,name,fallback_kind) VALUES(gen_random_uuid(),$1,'Foreign policy','direct') RETURNING id::text`, other)
	if _, err := repo.UpdateOwn(ctx, owner, sub.ID, Patch{RoutingProfileID: OptionalID{Set: true, Value: &foreignProfile}}, "test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign routing profile binding: %v", err)
	}
	if _, err := repo.UpdateOwn(ctx, owner, sub.ID, Patch{RoutingProfileID: OptionalID{Set: true, Value: &profile}}, "test"); err != nil {
		t.Fatalf("bind own routing profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO routing_rules(id,profile_id,priority,match_type,match_value,action,line_id) VALUES(gen_random_uuid(),$1,10,'domain','example.com','line',$2)`, profile, line); err != nil {
		t.Fatal(err)
	}
	if body, _, err := repo.Export(ctx, token, "mihomo"); err != nil || !strings.Contains(string(body), "DOMAIN,example.com,LINE:"+line) {
		t.Fatalf("routing export: %v %s", err, body)
	}
	otherLine := add(`INSERT INTO lines(id,name,created_by) VALUES(gen_random_uuid(),'Not subscribed',$1) RETURNING id::text`, owner)
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'egress')`, otherLine, node); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE routing_rules SET line_id=$2 WHERE profile_id=$1`, profile, otherLine); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Export(ctx, token, "mihomo"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("routing line absent from export: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE routing_rules SET line_id=$2 WHERE profile_id=$1`, profile, line); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE routing_profiles SET enabled=false WHERE id=$1`, profile); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Export(ctx, token, "mihomo"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled routing profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE routing_profiles SET enabled=true WHERE id=$1`, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateOwn(ctx, owner, sub.ID, Patch{RoutingProfileID: OptionalID{Set: true}}, "test"); err != nil {
		t.Fatalf("clear routing profile: %v", err)
	}
	if body, _, err := repo.Export(ctx, token, "mihomo"); err != nil || !strings.Contains(string(body), "MATCH,PROXY") {
		t.Fatalf("default export after clear: %v %s", err, body)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"192.0.2.5", "2001:db8::5"} {
		exec(`UPDATE nodes SET public_ip=$2 WHERE id=$1`, node, address)
		if body, _, err := repo.Export(ctx, token, "mihomo"); err != nil || !strings.Contains(string(body), address) {
			t.Fatalf("public IP export %s: %v", address, err)
		}
	}
	exec(`UPDATE agents SET last_seen_at=now()-interval '1 minute' WHERE node_id=$1`, node)
	if _, _, err := repo.Export(ctx, token, "sing-box"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale Agent export: %v", err)
	}
	exec(`UPDATE agents SET last_seen_at=now() WHERE node_id=$1`, node)
	exec(`UPDATE memberships SET snapshot_json=jsonb_set(snapshot_json,'{line_ids}','[]') WHERE user_id=$1`, owner)
	if _, _, err := repo.Export(ctx, token, "mihomo"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("revoked grant export: %v", err)
	}
	exec(`UPDATE memberships SET snapshot_json=$2 WHERE user_id=$1`, owner, snapshot)
	disabled := false
	if _, err := repo.UpdateOwn(ctx, owner, sub.ID, Patch{Enabled: &disabled}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Export(ctx, token, "mihomo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled export: %v", err)
	}
	enabled := true
	if _, err := repo.UpdateOwn(ctx, owner, sub.ID, Patch{Enabled: &enabled}, "test"); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE memberships SET ends_at=now()-interval '1 second' WHERE user_id=$1`, owner)
	if _, _, err := repo.Export(ctx, token, "mihomo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired export: %v", err)
	}
	exec(`UPDATE memberships SET ends_at=now()+interval '1 day' WHERE user_id=$1`, owner)
	if _, err := proxies.RotateOwn(ctx, owner, access.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Export(ctx, token, "mihomo"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unapplied rotation export: %v", err)
	}
	rotated, err := repo.RotateOwn(ctx, strings.ToUpper(owner), strings.ToUpper(sub.ID), "test")
	if err != nil || rotated == token {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := repo.ResolveToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token: %v", err)
	}
	if got, err := repo.RevealOwn(ctx, owner, sub.ID); err != nil || got != rotated {
		t.Fatalf("canonical reveal after alias rotation: %v", err)
	}
	if _, err := repo.ResolveToken(ctx, rotated); err != nil {
		t.Fatalf("new token: %v", err)
	}
	if err := repo.DeleteOwn(ctx, owner, sub.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ResolveToken(ctx, rotated); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted token: %v", err)
	}
	if _, err := repo.GetOwn(ctx, owner, sub.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"Concurrent A", "Concurrent B"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, _, err := repo.Create(ctx, owner, Input{Name: name, NameTemplate: input.NameTemplate, ProxyAccessIDs: input.ProxyAccessIDs, Enabled: true}, "test")
			results <- err
		}(name)
	}
	wg.Wait()
	close(results)
	okCount, limitCount := 0, 0
	for err := range results {
		if err == nil {
			okCount++
		} else if errors.Is(err, ErrLimit) {
			limitCount++
		} else {
			t.Fatal(err)
		}
	}
	if okCount != 1 || limitCount != 1 {
		t.Fatalf("concurrent quota: %d/%d", okCount, limitCount)
	}
	var audits string
	if err := pool.QueryRow(ctx, `SELECT coalesce(string_agg(after_json::text,' '),'') FROM audit_logs WHERE object_type='subscription'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audits, token) || strings.Contains(audits, rotated) || strings.Contains(audits, password) {
		t.Fatal("subscription audit leaked credentials")
	}
	var remaining string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM subscriptions WHERE user_id=$1 LIMIT 1`, owner).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	disabledAgain := false
	if _, err := repo.UpdateOwn(ctx, owner, remaining, Patch{Enabled: &disabledAgain}, "test"); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE memberships SET snapshot_json=jsonb_set(snapshot_json,'{line_ids}','[]') WHERE user_id=$1`, owner)
	ids := []string{access.ID}
	if _, err := repo.UpdateOwn(ctx, owner, remaining, Patch{ProxyAccessIDs: &ids}, "test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled target replacement without entitlement: %v", err)
	}
	exec(`DELETE FROM proxy_accesses WHERE id=$1`, access.ID)
	newName := "Metadata after target deletion"
	if _, err := repo.UpdateOwn(ctx, owner, remaining, Patch{Name: &newName}, "test"); err != nil {
		t.Fatalf("zero-target metadata edit: %v", err)
	}

}
