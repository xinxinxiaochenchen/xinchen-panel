package orchestration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRevisionStageAndApplyResults(t *testing.T) {
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
	var groupID, nodeID, ownerID, planID, policyID, ruleID string
	defer func() {
		if ownerID != "" {
			for _, query := range []string{
				`DELETE FROM forward_rules WHERE user_id=$1`,
				`DELETE FROM memberships WHERE user_id=$1`,
				`DELETE FROM users WHERE id=$1`,
			} {
				if _, err := pool.Exec(ctx, query, ownerID); err != nil {
					t.Errorf("cleanup revision owner: %v", err)
				}
			}
		}
		if policyID != "" {
			if _, err := pool.Exec(ctx, `DELETE FROM forward_target_policies WHERE id=$1`, policyID); err != nil {
				t.Errorf("cleanup revision policy: %v", err)
			}
		}
		if nodeID != "" {
			for _, query := range []string{
				`DELETE FROM config_revisions WHERE node_id=$1`,
				`DELETE FROM agents WHERE node_id=$1`,
				`DELETE FROM nodes WHERE id=$1`,
			} {
				if _, err := pool.Exec(ctx, query, nodeID); err != nil {
					t.Errorf("cleanup revision fixture: %v", err)
				}
			}
		}
		if groupID != "" {
			if _, err := pool.Exec(ctx, `DELETE FROM resource_groups WHERE id=$1`, groupID); err != nil {
				t.Errorf("cleanup revision group: %v", err)
			}
		}
		if planID != "" {
			if _, err := pool.Exec(ctx, `DELETE FROM plans WHERE id=$1`, planID); err != nil {
				t.Errorf("cleanup revision plan: %v", err)
			}
		}
	}()
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%X", random)
	if err := pool.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region)
VALUES (gen_random_uuid(),$1,'Revision','US') RETURNING id::text`, "TEST.REVISION."+suffix).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,capabilities)
VALUES (gen_random_uuid(),$1,'Revision node','US','revision.example.org',ARRAY['forward']) RETURNING id::text`, groupID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO agents(id,node_id) VALUES (gen_random_uuid(),$1)`, nodeID); err != nil {
		t.Fatal(err)
	}
	insertID := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ownerID = insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "revision-owner-"+suffix+"@example.invalid")
	planID = insertID(`INSERT INTO plans(id,name,quota_bytes) VALUES (gen_random_uuid(),$1,1000000) RETURNING id::text`, "revision-plan-"+suffix)
	grant, err := json.Marshal(map[string]any{"resource_group_ids": []string{groupID}, "limits": map[string]any{"max_forward_rules_per_node": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES (gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, ownerID, planID, grant); err != nil {
		t.Fatal(err)
	}
	policyID = insertID(`INSERT INTO forward_target_policies(id,kind,protocol,port_start,port_end)
VALUES (gen_random_uuid(),'public_host','TCP',1,65535) RETURNING id::text`)
	ruleID = insertID(`INSERT INTO forward_rules(id,user_id,name,ingress_node_id,ingress_port,target_host,target_port,protocol)
VALUES (gen_random_uuid(),$1,'Revision rule',$2,24000,'example.org',443,'TCP') RETURNING id::text`, ownerID, nodeID)
	repo := NewRevisionRepository(pool)
	first, created, err := repo.Reconcile(ctx, nodeID)
	if err != nil || !created || first.Revision != 1 {
		t.Fatalf("first revision = %+v, created=%t, error=%v", first, created, err)
	}
	if len(first.Snapshot.Rules) != 1 || first.Snapshot.Rules[0].ID != ruleID {
		t.Fatalf("revision did not use this node's database facts: %+v", first)
	}
	replayed, created, err := repo.Reconcile(ctx, nodeID)
	if err != nil || created || replayed.Revision != 1 || replayed.Digest != first.Digest {
		t.Fatalf("same payload created revision: %+v, created=%t, error=%v", replayed, created, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forward_rules SET target_port=8443 WHERE id=$1`, ruleID); err != nil {
		t.Fatal(err)
	}
	second, created, err := repo.Reconcile(ctx, nodeID)
	if err != nil || !created || second.Revision != 2 || second.Digest == first.Digest {
		t.Fatalf("changed payload revision = %+v, created=%t, error=%v", second, created, err)
	}
	latest, err := repo.Desired(ctx, nodeID)
	if err != nil || latest.Revision != 2 || latest.Snapshot.Rules[0].TargetPort != 8443 {
		t.Fatalf("desired revision = %+v, %v", latest, err)
	}
	if err := repo.RecordResult(ctx, nodeID, 2, first.Digest, "applied", "", ""); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("wrong digest ACK = %v", err)
	}
	if err := repo.RecordResult(ctx, nodeID, 9, second.Digest, "applied", "", ""); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("unknown revision ACK = %v", err)
	}
	if err := repo.RecordResult(ctx, nodeID, 2, second.Digest, "rejected", "BIND_FAILED", "port unavailable"); err != nil {
		t.Fatal(err)
	}
	var applied int64
	if err := pool.QueryRow(ctx, `SELECT applied_revision FROM agents WHERE node_id=$1`, nodeID).Scan(&applied); err != nil || applied != 0 {
		t.Fatalf("NACK advanced applied revision: %d, %v", applied, err)
	}
	if err := repo.RecordResult(ctx, nodeID, 1, first.Digest, "applied", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordResult(ctx, nodeID, 2, second.Digest, "applied", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordResult(ctx, nodeID, 2, second.Digest, "applied", "", ""); err != nil {
		t.Fatalf("duplicate applied ACK = %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT applied_revision FROM agents WHERE node_id=$1`, nodeID).Scan(&applied); err != nil || applied != 2 {
		t.Fatalf("applied revision = %d, %v", applied, err)
	}
	if err := repo.RecordResult(ctx, nodeID, 1, first.Digest, "rejected", "BIND_FAILED", "old result"); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("stale conflicting NACK = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forward_rules SET target_port=9443 WHERE id=$1`, ruleID); err != nil {
		t.Fatal(err)
	}
	third, created, err := repo.Reconcile(ctx, nodeID)
	if err != nil || !created || third.Revision != 3 {
		t.Fatalf("third revision = %+v, created=%t, error=%v", third, created, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forward_rules SET target_port=10443 WHERE id=$1`, ruleID); err != nil {
		t.Fatal(err)
	}
	fourth, created, err := repo.Reconcile(ctx, nodeID)
	if err != nil || !created || fourth.Revision != 4 {
		t.Fatalf("fourth revision = %+v, created=%t, error=%v", fourth, created, err)
	}
	if err := repo.RecordResult(ctx, nodeID, 4, fourth.Digest, "applied", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordResult(ctx, nodeID, 3, third.Digest, "applied", "", ""); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("out-of-order older ACK after newer apply = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json='"bad"'::jsonb WHERE user_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	revoked, created, err := repo.Reconcile(ctx, nodeID)
	if err != nil || !created || revoked.Revision != 5 || len(revoked.Snapshot.Rules) != 0 || len(revoked.Diagnostics) != 1 {
		t.Fatalf("revocation with diagnostics = %+v, created=%t, error=%v", revoked, created, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forward_rules SET enabled=false WHERE id=$1`, ruleID); err != nil {
		t.Fatal(err)
	}
	unchanged, created, err := repo.Reconcile(ctx, nodeID)
	if err != nil || created || unchanged.Revision != 5 || len(unchanged.Diagnostics) != 0 {
		t.Fatalf("same executable payload did not refresh diagnostics: %+v, created=%t, error=%v", unchanged, created, err)
	}
	if err := repo.RecordResult(ctx, nodeID, 5, revoked.Digest, "rejected", "BIND_FAILED", "first attempt"); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordResult(ctx, nodeID, 5, revoked.Digest, "rejected", "BIND_FAILED", "second attempt"); err != nil {
		t.Fatalf("new rejection for same pending version: %v", err)
	}
	latest, err = repo.Desired(ctx, nodeID)
	if err != nil || latest.ErrorMessage == nil || *latest.ErrorMessage != "second attempt" || len(latest.Diagnostics) != 0 {
		t.Fatalf("latest rejection not persisted: %+v, %v", latest, err)
	}
	if err := repo.RecordResult(ctx, nodeID, 5, revoked.Digest, "rejected", "BAD\nCODE", "message"); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("unsafe error code accepted: %v", err)
	}
	if err := repo.RecordResult(ctx, nodeID, 5, revoked.Digest, "rejected", "BIND_FAILED", strings.Repeat("x", 1025)); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("oversize error message accepted: %v", err)
	}
	if err := repo.RecordResult(ctx, nodeID, 5, revoked.Digest, "rejected", strings.Repeat("A", 65), "message"); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("oversize error code accepted: %v", err)
	}
	if err := repo.RecordResult(ctx, nodeID, 5, revoked.Digest, "rejected", "BIND_FAILED", "bad\nmessage"); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("control character in error message accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json=$2 WHERE user_id=$1`, ownerID, grant); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forward_rules SET enabled=true WHERE id=$1`, ruleID); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	start := make(chan struct{})
	type result struct {
		value   DesiredRevision
		created bool
		err     error
	}
	results := make(chan result, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			value, created, err := repo.Reconcile(ctx, nodeID)
			results <- result{value, created, err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	createdCount := 0
	for got := range results {
		if got.err != nil || got.value.Revision != 6 || len(got.value.Snapshot.Rules) != 1 {
			t.Fatalf("concurrent reconcile = %+v", got)
		}
		if got.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent workers created %d revisions, want 1", createdCount)
	}
}
