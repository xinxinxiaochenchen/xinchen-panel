package orchestration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresProxyRevisionLifecycle(t *testing.T) {
	databaseURL := orchestrationTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	insert := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	owner := insert(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),'proxy-revision@example.invalid','hash','active') RETURNING id::text`)
	group := insert(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),'TEST.PROXY.REVISION','Proxy revision','US') RETURNING id::text`)
	node := insert(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities) VALUES(gen_random_uuid(),$1,'Proxy revision','US','example.org',24443,ARRAY['proxy']) RETURNING id::text`, group)
	line := insert(`INSERT INTO lines(id,name,created_by) VALUES(gen_random_uuid(),'Proxy revision',$1) RETURNING id::text`, owner)
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'egress')`, line, node); err != nil {
		t.Fatal(err)
	}
	plan := insert(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),'proxy-revision',1000000) RETURNING id::text`)
	grant, _ := json.Marshal(map[string]any{"resource_group_ids": []string{group}, "line_ids": []string{line}})
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, owner, plan, grant); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agents(id,node_id) VALUES(gen_random_uuid(),$1)`, node); err != nil {
		t.Fatal(err)
	}
	access := insert(`INSERT INTO proxy_accesses(id,user_id,line_id,name,credential_hash,credential_ciphertext) VALUES(gen_random_uuid(),$1,$2,'Revision access',$3,$4) RETURNING id::text`, owner, line, strings.Repeat("a", 56), strings.Repeat("x", 40))
	repo := NewRevisionRepository(pool)
	legacy, changed, err := repo.Reconcile(ctx, node)
	if err != nil || !changed || len(legacy.Snapshot.ProxyConfig) != 0 {
		t.Fatalf("forward-only Agent received proxy: %+v, %v", legacy, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agents SET capabilities=ARRAY['forward','proxy'] WHERE node_id=$1`, node); err != nil {
		t.Fatal(err)
	}
	first, changed, err := repo.Reconcile(ctx, node)
	if err != nil || !changed || len(first.Snapshot.ProxyConfig) != 1 || first.Snapshot.ProxyConfig[0].ID != access {
		t.Fatalf("proxy revision: %+v, changed=%t, %v", first, changed, err)
	}
	if err := repo.RecordResult(ctx, node, first.Revision, first.Digest, "rejected", "APPLY_FAILED", "configuration could not be applied"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT apply_status FROM proxy_accesses WHERE id=$1`, access).Scan(&status); err != nil || status != "apply_failed" {
		t.Fatalf("NACK status=%s, %v", status, err)
	}
	if err := repo.RecordResult(ctx, node, first.Revision, first.Digest, "applied", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT apply_status FROM proxy_accesses WHERE id=$1`, access).Scan(&status); err != nil || status != "active" {
		t.Fatalf("ACK status=%s, %v", status, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE proxy_accesses SET name='Renamed',apply_status='pending',updated_at=clock_timestamp() WHERE id=$1`, access); err != nil {
		t.Fatal(err)
	}
	unchanged, changed, err := repo.Reconcile(ctx, node)
	if err != nil || changed || unchanged.Revision != first.Revision {
		t.Fatalf("metadata edit created revision: %+v, changed=%t, %v", unchanged, changed, err)
	}
	if err := pool.QueryRow(ctx, `SELECT apply_status FROM proxy_accesses WHERE id=$1`, access).Scan(&status); err != nil || status != "active" {
		t.Fatalf("metadata edit stayed pending: %s, %v", status, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE proxy_accesses SET credential_hash=$2,apply_status='pending' WHERE id=$1`, access, strings.Repeat("b", 56)); err != nil {
		t.Fatal(err)
	}
	second, changed, err := repo.Reconcile(ctx, node)
	if err != nil || !changed || second.Digest == first.Digest || second.Snapshot.ProxyConfig[0].CredentialHash != strings.Repeat("b", 56) {
		t.Fatalf("rotated revision: %+v, %v", second, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE proxy_accesses SET credential_hash=$2,apply_status='pending',updated_at=clock_timestamp() WHERE id=$1`, access, strings.Repeat("c", 56)); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordResult(ctx, node, second.Revision, second.Digest, "applied", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT apply_status FROM proxy_accesses WHERE id=$1`, access).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("old ACK changed rotated credential status=%s, %v", status, err)
	}
	third, changed, err := repo.Reconcile(ctx, node)
	if err != nil || !changed || third.Snapshot.ProxyConfig[0].CredentialHash != strings.Repeat("c", 56) {
		t.Fatalf("new credential revision: %+v, %v", third, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET ends_at=now()-interval '1 second' WHERE user_id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	revoked, changed, err := repo.Reconcile(ctx, node)
	if err != nil || !changed || len(revoked.Snapshot.ProxyConfig) != 0 {
		t.Fatalf("expired membership revision: %+v, %v", revoked, err)
	}
	if err := repo.RecordResult(ctx, node, revoked.Revision, revoked.Digest, "applied", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT apply_status FROM proxy_accesses WHERE id=$1`, access).Scan(&status); err != nil || status != "disabled" {
		t.Fatalf("revocation status=%s, %v", status, err)
	}
}
