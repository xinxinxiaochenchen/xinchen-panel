package forward

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresTargetPoliciesAreAuditedAndDefaultDeny(t *testing.T) {
	databaseURL := forwardTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var adminID, groupID string
	if err := pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'policy-admin@example.invalid','hash','active') RETURNING id::text`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region) VALUES (gen_random_uuid(),'TEST.POLICY','Policy Group','US') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool)
	policy, err := repo.CreateTargetPolicy(ctx, TargetPolicyInput{Kind: "node", TargetGroupID: &groupID,
		Protocol: "TCP", PortStart: 443, PortEnd: 443, Enabled: true}, adminID, "create-policy")
	if err != nil || policy.ID == "" || policy.TargetGroupID == nil || *policy.TargetGroupID != groupID {
		t.Fatalf("created policy = %+v, %v", policy, err)
	}
	if _, err := repo.CreateTargetPolicy(ctx, TargetPolicyInput{Kind: "node", TargetGroupID: &groupID,
		Protocol: "TCP", PortStart: 443, PortEnd: 443, Enabled: true}, adminID, "duplicate-policy"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate policy = %v", err)
	}
	if policies, err := repo.ListTargetPolicies(ctx, 10, ""); err != nil || len(policies) == 0 {
		t.Fatalf("policy list = %+v, %v", policies, err)
	}
	if got, err := repo.GetTargetPolicy(ctx, policy.ID); err != nil || got.ID != policy.ID {
		t.Fatalf("policy detail = %+v, %v", got, err)
	}
	var ingressID, targetID, ruleID string
	if err := pool.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,capabilities) VALUES (gen_random_uuid(),$1,'Policy Ingress','US','policy-ingress.example.invalid',ARRAY['forward']) RETURNING id::text`, groupID).Scan(&ingressID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities) VALUES (gen_random_uuid(),$1,'Policy Target','US','policy-target.example.invalid',443,ARRAY['proxy']) RETURNING id::text`, groupID).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO forward_rules(id,user_id,name,ingress_node_id,ingress_port,target_node_id,target_port,protocol,apply_status)
VALUES (gen_random_uuid(),$1,'Policy Test Rule',$2,28000,$3,443,'TCP','active') RETURNING id::text`, adminID, ingressID, targetID).Scan(&ruleID); err != nil {
		t.Fatal(err)
	}
	disabled := false
	updated, err := repo.UpdateTargetPolicy(ctx, policy.ID, TargetPolicyPatch{Enabled: &disabled}, adminID, "disable-policy")
	if err != nil || updated.Enabled {
		t.Fatalf("disabled policy = %+v, %v", updated, err)
	}
	var ruleStatus string
	if err := pool.QueryRow(ctx, `SELECT apply_status FROM forward_rules WHERE id=$1`, ruleID).Scan(&ruleStatus); err != nil || ruleStatus != "pending" {
		t.Fatalf("rule after policy disable = %q, %v", ruleStatus, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forward_rules SET apply_status='active' WHERE id=$1`, ruleID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteTargetPolicy(ctx, policy.ID, adminID, "delete-policy"); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT apply_status FROM forward_rules WHERE id=$1`, ruleID).Scan(&ruleStatus); err != nil || ruleStatus != "pending" {
		t.Fatalf("rule after policy deletion = %q, %v", ruleStatus, err)
	}
	if _, err := repo.GetTargetPolicy(ctx, policy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted policy = %v", err)
	}
	var audits, events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE object_type='forward_target_policy' AND object_id=$1`, policy.ID).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("policy audit rows = %d, %v", audits, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE kind='forward_policy.changed' AND aggregate_id=$1`, policy.ID).Scan(&events); err != nil || events != 3 {
		t.Fatalf("policy outbox rows = %d, %v", events, err)
	}
}
