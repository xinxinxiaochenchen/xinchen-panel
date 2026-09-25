package forward

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func forwardTestDatabaseURL() string {
	if value := os.Getenv("CONTROL_TEST_DATABASE_URL"); value != "" {
		return value
	}
	if os.Getenv("CONTROL_TEST_DB_NAME") != "" && os.Getenv("POSTGRES_PASSWORD") != "" {
		return (&url.URL{Scheme: "postgres", User: url.UserPassword("controlplane", os.Getenv("POSTGRES_PASSWORD")), Host: "db:5432", Path: "/" + os.Getenv("CONTROL_TEST_DB_NAME")}).String()
	}
	return ""
}

func TestPostgresForwardRuleReservationAndEntitlement(t *testing.T) {
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
	var ownerID, otherID, groupID, ingressID, targetID, planID string
	insertID := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ownerID = insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'forward-owner@example.invalid','hash','active') RETURNING id::text`)
	otherID = insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'forward-other@example.invalid','hash','active') RETURNING id::text`)
	groupID = insertID(`INSERT INTO resource_groups(id,code,name,region) VALUES (gen_random_uuid(),'TEST.FWD','Forward Group','US') RETURNING id::text`)
	ingressID = insertID(`INSERT INTO nodes(id,group_id,name,region,host,capabilities) VALUES (gen_random_uuid(),$1,'Ingress','US','ingress.example.invalid',ARRAY['forward']) RETURNING id::text`, groupID)
	targetID = insertID(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities) VALUES (gen_random_uuid(),$1,'Target','US','target.example.invalid',443,ARRAY['proxy']) RETURNING id::text`, groupID)
	planID = insertID(`INSERT INTO plans(id,name,quota_bytes) VALUES (gen_random_uuid(),'forward-test-plan',1000000) RETURNING id::text`)
	snapshot, err := json.Marshal(map[string]any{"resource_group_ids": []string{groupID}, "limits": map[string]any{"max_forward_rules_per_node": 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{ownerID, otherID} {
		if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES (gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, userID, planID, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewPostgresRepository(pool)
	host := "example.org"
	input := RuleInput{Name: "HTTP", IngressNodeID: ingressID, IngressPort: 24000,
		TargetHost: &host, TargetPort: 443, Protocol: "BOTH", Enabled: true}
	noMembershipID := insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'forward-no-plan@example.invalid','hash','active') RETURNING id::text`)
	if _, err := repo.CreateRule(ctx, input, noMembershipID, "no-membership"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rule without membership = %v", err)
	}
	if _, err := repo.CreateRule(ctx, input, ownerID, "default-deny"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("rule without target policy = %v", err)
	}
	insertID(`INSERT INTO forward_target_policies(id,kind,protocol,port_start,port_end) VALUES (gen_random_uuid(),'public_host','TCP',443,443) RETURNING id::text`)
	if _, err := repo.CreateRule(ctx, input, ownerID, "both-needs-udp"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("BOTH rule without UDP policy = %v", err)
	}
	insertID(`INSERT INTO forward_target_policies(id,kind,protocol,port_start,port_end) VALUES (gen_random_uuid(),'public_host','UDP',443,443) RETURNING id::text`)
	rule, err := repo.CreateRule(ctx, input, ownerID, "create-forward")
	if err != nil || rule.UserID != ownerID || rule.ApplyStatus != "pending" || rule.Protocol != "BOTH" {
		t.Fatalf("created rule = %+v, %v", rule, err)
	}
	var activePorts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM port_allocations WHERE owner_id=$1 AND released_at IS NULL`, rule.ID).Scan(&activePorts); err != nil || activePorts != 2 {
		t.Fatalf("BOTH allocations = %d, %v", activePorts, err)
	}
	if _, err := repo.CreateRule(ctx, RuleInput{Name: "Second", IngressNodeID: ingressID, IngressPort: 24001,
		TargetHost: &host, TargetPort: 443, Protocol: "TCP", Enabled: false}, ownerID, "limit-forward"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("second rule beyond node limit = %v", err)
	}
	if _, err := repo.CreateRule(ctx, RuleInput{Name: "Conflict", IngressNodeID: ingressID, IngressPort: 24000,
		TargetHost: &host, TargetPort: 443, Protocol: "TCP", Enabled: true}, otherID, "port-conflict"); !errors.Is(err, ErrConflict) {
		t.Fatalf("occupied port = %v", err)
	}
	if _, err := repo.GetOwnRule(ctx, otherID, rule.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner read = %v", err)
	}
	if got, err := repo.GetOwnRule(ctx, ownerID, rule.ID); err != nil || got.ID != rule.ID {
		t.Fatalf("own detail = %+v, %v", got, err)
	}
	if rules, err := repo.ListOwnRules(ctx, ownerID, 10, ""); err != nil || len(rules) != 1 {
		t.Fatalf("own list = %+v, %v", rules, err)
	}
	disabled := false
	updated, err := repo.UpdateOwnRule(ctx, ownerID, rule.ID, RulePatch{Enabled: &disabled}, "disable-forward")
	if err != nil || updated.Enabled || updated.ApplyStatus != "pending" {
		t.Fatalf("disabled rule = %+v, %v", updated, err)
	}
	if _, err := repo.CreateRule(ctx, RuleInput{Name: "Still Occupied", IngressNodeID: ingressID, IngressPort: 24000,
		TargetHost: &host, TargetPort: 443, Protocol: "UDP", Enabled: true}, otherID, "disabled-port-conflict"); !errors.Is(err, ErrConflict) {
		t.Fatalf("disabled rule released port early = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET ends_at=clock_timestamp()-interval '1 second' WHERE user_id=$1`, ownerID); err != nil {
		t.Fatal(err)
	}
	if expiredRules, err := repo.ListOwnRules(ctx, ownerID, 10, ""); err != nil || len(expiredRules) != 1 {
		t.Fatalf("expired owner cannot manage existing rule = %+v, %v", expiredRules, err)
	}
	enabled := true
	if _, err := repo.UpdateOwnRule(ctx, ownerID, rule.ID, RulePatch{Enabled: &enabled}, "expired-reenable"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired owner re-enabled = %v", err)
	}
	if err := repo.DeleteOwnRule(ctx, ownerID, rule.ID, "delete-forward"); err != nil {
		t.Fatalf("expired owner deletion = %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM port_allocations WHERE owner_id=$1 AND released_at IS NULL`, rule.ID).Scan(&activePorts); err != nil || activePorts != 0 {
		t.Fatalf("deleted rule retained active ports = %d, %v", activePorts, err)
	}
	if _, err := repo.GetOwnRule(ctx, ownerID, rule.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted rule readable = %v", err)
	}
	if _, err := repo.CreateRule(ctx, RuleInput{Name: "To Node", IngressNodeID: ingressID, IngressPort: 24002,
		TargetNodeID: &targetID, TargetPort: 443, Protocol: "UDP", Enabled: true}, otherID, "node-default-deny"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("target node without policy = %v", err)
	}
	nodePolicyID := insertID(`INSERT INTO forward_target_policies(id,kind,target_group_id,protocol,port_start,port_end) VALUES (gen_random_uuid(),'node',$1,'UDP',443,443) RETURNING id::text`, groupID)
	targetRule, err := repo.CreateRule(ctx, RuleInput{Name: "To Node", IngressNodeID: ingressID, IngressPort: 24002,
		TargetNodeID: &targetID, TargetPort: 443, Protocol: "UDP", Enabled: true}, otherID, "target-node")
	if err != nil || targetRule.TargetNodeID == nil || *targetRule.TargetNodeID != targetID {
		t.Fatalf("target-node rule = %+v, %v", targetRule, err)
	}
	if _, err := repo.UpdateOwnRule(ctx, otherID, targetRule.ID, RulePatch{Enabled: &disabled}, "disable-target-rule"); err != nil {
		t.Fatalf("disable target-node rule = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, otherID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateOwnRule(ctx, otherID, targetRule.ID, RulePatch{Enabled: &enabled}, "disabled-account-reenable"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled account re-enabled forward rule = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1`, otherID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forward_target_policies SET enabled=false WHERE id=$1`, nodePolicyID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateOwnRule(ctx, otherID, targetRule.ID, RulePatch{Enabled: &enabled}, "revoked-policy-reenable"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("rule re-enabled after policy revocation = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forward_target_policies SET enabled=true WHERE id=$1`, nodePolicyID); err != nil {
		t.Fatal(err)
	}
	if reenabled, err := repo.UpdateOwnRule(ctx, otherID, targetRule.ID, RulePatch{Enabled: &enabled}, "reenable-target-rule"); err != nil || !reenabled.Enabled || reenabled.ApplyStatus != "pending" {
		t.Fatalf("re-enable target-node rule = %+v, %v", reenabled, err)
	}
	thirdID := insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'forward-third@example.invalid','hash','active') RETURNING id::text`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES (gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, thirdID, planID, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRule(ctx, RuleInput{Name: "Partial BOTH", IngressNodeID: ingressID, IngressPort: 24002,
		TargetHost: &host, TargetPort: 443, Protocol: "BOTH", Enabled: true}, thirdID, "partial-both"); !errors.Is(err, ErrConflict) {
		t.Fatalf("BOTH conflict = %v", err)
	}
	var partialTCP int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM port_allocations WHERE node_id=$1 AND port=24002 AND protocol='TCP' AND released_at IS NULL`, ingressID).Scan(&partialTCP); err != nil || partialTCP != 0 {
		t.Fatalf("partial BOTH allocation = %d, %v", partialTCP, err)
	}
	otherGroup := insertID(`INSERT INTO resource_groups(id,code,name,region) VALUES (gen_random_uuid(),'TEST.FWD.DENIED','Denied','US') RETURNING id::text`)
	deniedTarget := insertID(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities) VALUES (gen_random_uuid(),$1,'Denied Target','US','denied.example.invalid',443,ARRAY['proxy']) RETURNING id::text`, otherGroup)
	deniedIngress := insertID(`INSERT INTO nodes(id,group_id,name,region,host,capabilities) VALUES (gen_random_uuid(),$1,'Denied Ingress','US','denied-ingress.example.invalid',ARRAY['forward']) RETURNING id::text`, otherGroup)
	if _, err := repo.CreateRule(ctx, RuleInput{Name: "Denied Ingress", IngressNodeID: deniedIngress, IngressPort: 24003,
		TargetHost: &host, TargetPort: 443, Protocol: "TCP", Enabled: true}, thirdID, "denied-ingress"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungranted ingress group = %v", err)
	}
	if _, err := repo.CreateRule(ctx, RuleInput{Name: "Denied Target", IngressNodeID: ingressID, IngressPort: 24003,
		TargetNodeID: &deniedTarget, TargetPort: 443, Protocol: "TCP", Enabled: true}, thirdID, "denied-target"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungranted target group = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=false WHERE id=$1`, targetID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRule(ctx, RuleInput{Name: "Offline Target", IngressNodeID: ingressID, IngressPort: 24003,
		TargetNodeID: &targetID, TargetPort: 443, Protocol: "TCP", Enabled: true}, thirdID, "disabled-target"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled target = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=true WHERE id=$1`, targetID); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ sql, name string }{
		{`UPDATE nodes SET enabled=false WHERE id=$1`, "disabled node"},
		{`UPDATE nodes SET capabilities=ARRAY['proxy']::text[],proxy_port=443 WHERE id=$1`, "non-forward node"},
		{`UPDATE resource_groups SET enabled=false WHERE id=$1`, "disabled group"},
	} {
		id := ingressID
		if change.name == "disabled group" {
			id = groupID
		}
		if _, err := pool.Exec(ctx, change.sql, id); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateRule(ctx, RuleInput{Name: change.name, IngressNodeID: ingressID, IngressPort: 24004,
			TargetHost: &host, TargetPort: 443, Protocol: "TCP", Enabled: true}, thirdID, change.name); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s create = %v", change.name, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=true,capabilities=ARRAY['forward']::text[],proxy_port=NULL WHERE id=$1`, ingressID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE resource_groups SET enabled=true WHERE id=$1`, groupID); err != nil {
			t.Fatal(err)
		}
	}
	freedRule, err := repo.CreateRule(ctx, RuleInput{Name: "Freed Port", IngressNodeID: ingressID, IngressPort: 24000,
		TargetHost: &host, TargetPort: 443, Protocol: "BOTH", Enabled: true}, thirdID, "freed-port")
	if err != nil {
		t.Fatalf("released BOTH port could not be reused: %v", err)
	}
	if _, err := repo.UpdateOwnRule(ctx, otherID, freedRule.ID, RulePatch{Enabled: &disabled}, "other-owner-update"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner updated rule = %v", err)
	}
	if err := repo.DeleteOwnRule(ctx, otherID, freedRule.ID, "other-owner-delete"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other owner deleted rule = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json=jsonb_set(snapshot_json,'{limits,max_forward_rules_per_node}','2') WHERE user_id=$1`, thirdID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRule(ctx, RuleInput{Name: "Second Name", IngressNodeID: ingressID, IngressPort: 24005,
		TargetHost: &host, TargetPort: 443, Protocol: "TCP", Enabled: true}, thirdID, "second-name"); err != nil {
		t.Fatal(err)
	}
	duplicateName := "Second Name"
	if _, err := repo.UpdateOwnRule(ctx, thirdID, freedRule.ID, RulePatch{Name: &duplicateName}, "duplicate-name"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate rule name = %v", err)
	}
	parallelID := insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'forward-parallel@example.invalid','hash','active') RETURNING id::text`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES (gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, parallelID, planID, snapshot); err != nil {
		t.Fatal(err)
	}
	var gate sync.WaitGroup
	gate.Add(1)
	results := make(chan error, 2)
	for i, name := range []string{"Parallel A", "Parallel B"} {
		go func(i int, name string) {
			gate.Wait()
			_, err := repo.CreateRule(ctx, RuleInput{Name: name, IngressNodeID: ingressID, IngressPort: 25000 + i,
				TargetHost: &host, TargetPort: 443, Protocol: "TCP", Enabled: true}, parallelID, name)
			results <- err
		}(i, name)
	}
	gate.Done()
	var created, limited int
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			created++
		case errors.Is(err, ErrLimitReached):
			limited++
		default:
			t.Fatalf("parallel create = %v", err)
		}
	}
	if created != 1 || limited != 1 {
		t.Fatalf("parallel rules created=%d limited=%d", created, limited)
	}
	portRacers := []string{
		insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'forward-port-racer-a@example.invalid','hash','active') RETURNING id::text`),
		insertID(`INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),'forward-port-racer-b@example.invalid','hash','active') RETURNING id::text`),
	}
	for _, racerID := range portRacers {
		if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES (gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, racerID, planID, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	gate.Add(1)
	for i, racerID := range portRacers {
		go func(i int, racerID string) {
			gate.Wait()
			_, err := repo.CreateRule(ctx, RuleInput{Name: "Port Race", IngressNodeID: ingressID, IngressPort: 27000,
				TargetHost: &host, TargetPort: 443, Protocol: "TCP", Enabled: true}, racerID, "port-race")
			results <- err
		}(i, racerID)
	}
	gate.Done()
	created, limited = 0, 0
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			created++
		case errors.Is(err, ErrConflict):
			limited++
		default:
			t.Fatalf("parallel port claim = %v", err)
		}
	}
	if created != 1 || limited != 1 {
		t.Fatalf("parallel port claims created=%d conflicted=%d", created, limited)
	}
	var auditCount, outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE object_type='forward_rule' AND object_id=$1`, rule.ID).Scan(&auditCount); err != nil || auditCount != 3 {
		t.Fatalf("create/update/delete audit count = %d, %v", auditCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE kind='forward_rule.changed' AND aggregate_id=$1`, rule.ID).Scan(&outboxCount); err != nil || outboxCount != 3 {
		t.Fatalf("create/update/delete outbox count = %d, %v", outboxCount, err)
	}
}
