package orchestration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func orchestrationTestDatabaseURL() string {
	if value := os.Getenv("CONTROL_TEST_DATABASE_URL"); value != "" {
		return value
	}
	if os.Getenv("CONTROL_TEST_DB_NAME") != "" && os.Getenv("POSTGRES_PASSWORD") != "" {
		return (&url.URL{Scheme: "postgres", User: url.UserPassword("controlplane", os.Getenv("POSTGRES_PASSWORD")),
			Host: "db:5432", Path: "/" + os.Getenv("CONTROL_TEST_DB_NAME")}).String()
	}
	return ""
}

func TestPostgresForwardSnapshotOmitsRevokedRules(t *testing.T) {
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
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%X", random)
	var groupID, ownerID, planID, policyID string
	defer func() {
		cleanup := func(query string, args ...any) {
			if _, err := pool.Exec(ctx, query, args...); err != nil {
				t.Errorf("cleanup isolated compiler fixture: %v", err)
			}
		}
		if ownerID != "" {
			cleanup(`DELETE FROM forward_rules WHERE user_id=$1`, ownerID)
			cleanup(`DELETE FROM memberships WHERE user_id=$1`, ownerID)
			cleanup(`DELETE FROM users WHERE id=$1`, ownerID)
		}
		if policyID != "" {
			cleanup(`DELETE FROM forward_target_policies WHERE id=$1`, policyID)
		}
		if groupID != "" {
			cleanup(`DELETE FROM forward_target_policies WHERE target_group_id=$1`, groupID)
			cleanup(`DELETE FROM nodes WHERE group_id=$1`, groupID)
			cleanup(`DELETE FROM resource_groups WHERE id=$1`, groupID)
		}
		if planID != "" {
			cleanup(`DELETE FROM plans WHERE id=$1`, planID)
		}
	}()
	insertID := func(query string, args ...any) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	groupID = insertID(`INSERT INTO resource_groups(id,code,name,region) VALUES (gen_random_uuid(),$1,'Compile','US') RETURNING id::text`, "TEST.COMPILE."+suffix)
	nodeID := insertID(`INSERT INTO nodes(id,group_id,name,region,host,capabilities) VALUES (gen_random_uuid(),$1,'Ingress','US','ingress.example.org',ARRAY['forward']) RETURNING id::text`, groupID)
	ownerID = insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "compile-owner-"+suffix+"@example.invalid")
	planID = insertID(`INSERT INTO plans(id,name,quota_bytes) VALUES (gen_random_uuid(),$1,1000000) RETURNING id::text`, "compile-plan-"+suffix)
	memberSnapshot, err := json.Marshal(map[string]any{"resource_group_ids": []string{groupID}, "limits": map[string]any{"max_forward_rules_per_node": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES (gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, ownerID, planID, memberSnapshot); err != nil {
		t.Fatal(err)
	}
	policyID = insertID(`INSERT INTO forward_target_policies(id,kind,protocol,port_start,port_end)
VALUES (gen_random_uuid(),'public_host','TCP',14443,14443) RETURNING id::text`)
	validID := insertID(`INSERT INTO forward_rules(id,user_id,name,ingress_node_id,ingress_port,target_host,target_port,protocol)
VALUES (gen_random_uuid(),$1,'Valid',$2,24000,'example.org',14443,'TCP') RETURNING id::text`, ownerID, nodeID)
	_ = insertID(`INSERT INTO forward_rules(id,user_id,name,ingress_node_id,ingress_port,target_host,target_port,protocol)
VALUES (gen_random_uuid(),$1,'No policy',$2,24001,'example.org',80,'TCP') RETURNING id::text`, ownerID, nodeID)
	repo := NewForwardSnapshotRepository(pool)
	compiled, err := repo.Compile(ctx, nodeID, 3)
	if err != nil || len(compiled.Snapshot.Rules) != 1 || compiled.Snapshot.Rules[0].ID != validID {
		t.Fatalf("initial compiled rules = %+v, %v", compiled, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json='"bad"'::jsonb WHERE user_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	compiled, err = repo.Compile(ctx, nodeID, 31)
	if err != nil || len(compiled.Snapshot.Rules) != 0 || len(compiled.Rejected) != 2 {
		t.Fatalf("malformed membership blocked safe snapshot: %+v, %v", compiled, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json=$2 WHERE user_id=$1`, ownerID, memberSnapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forward_target_policies SET enabled=false WHERE id=$1`, policyID); err != nil {
		t.Fatal(err)
	}
	compiled, err = repo.Compile(ctx, nodeID, 4)
	if err != nil || len(compiled.Snapshot.Rules) != 0 {
		t.Fatalf("revoked policy still compiled: %+v, %v", compiled, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forward_target_policies SET enabled=true WHERE id=$1`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET ends_at=now()-interval '1 second' WHERE user_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	compiled, err = repo.Compile(ctx, nodeID, 5)
	if err != nil || len(compiled.Snapshot.Rules) != 0 {
		t.Fatalf("expired membership still compiled: %+v, %v", compiled, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=false WHERE id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	compiled, err = repo.Compile(ctx, nodeID, 6)
	if err != nil || len(compiled.Snapshot.Rules) != 0 {
		t.Fatalf("disabled node still compiled: %+v, %v", compiled, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json='"bad"'::jsonb WHERE user_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	compiled, err = repo.Compile(ctx, nodeID, 7)
	if err != nil || len(compiled.Snapshot.Rules) != 0 {
		t.Fatalf("disabled node could not revoke malformed membership: %+v, %v", compiled, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json=$2 WHERE user_id=$1`, ownerID, memberSnapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=true WHERE id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET ends_at=now()+interval '1 day' WHERE user_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	targetID := insertID(`INSERT INTO nodes(id,group_id,name,region,host,public_ip,proxy_port,capabilities)
VALUES (gen_random_uuid(),$1,'Target','US','10.0.0.5','8.8.8.8',443,ARRAY['proxy']) RETURNING id::text`, groupID)
	_ = insertID(`INSERT INTO forward_target_policies(id,kind,target_group_id,protocol,port_start,port_end)
VALUES (gen_random_uuid(),'node',$1,'TCP',14443,14443) RETURNING id::text`, groupID)
	targetRuleID := insertID(`INSERT INTO forward_rules(id,user_id,name,ingress_node_id,ingress_port,target_node_id,target_port,protocol)
VALUES (gen_random_uuid(),$1,'To public node IP',$2,24002,$3,14443,'TCP') RETURNING id::text`, ownerID, nodeID, targetID)
	compiled, err = repo.Compile(ctx, nodeID, 7)
	if err != nil || len(compiled.Snapshot.Rules) != 2 {
		t.Fatalf("public target-node IP did not compile: %+v, %v", compiled, err)
	}
	if compiled.Snapshot.Rules[0].ID == targetRuleID && compiled.Snapshot.Rules[0].TargetHost != "8.8.8.8" ||
		compiled.Snapshot.Rules[1].ID == targetRuleID && compiled.Snapshot.Rules[1].TargetHost != "8.8.8.8" {
		t.Fatalf("target node compiled with private host: %+v", compiled)
	}
}
