package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdmissionInputRejectsUntrustedAttribution(t *testing.T) {
	valid := OpenRequest{ConnectionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", RequestID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", ResourceID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", ResourceKind: "forward", Revision: 1, RequestedBytes: 128}
	if err := validateOpenRequest(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*OpenRequest){func(v *OpenRequest) { v.ConnectionID = "bad" }, func(v *OpenRequest) { v.RequestID = "" }, func(v *OpenRequest) { v.ResourceID = "bad" }, func(v *OpenRequest) { v.ResourceKind = "user" }, func(v *OpenRequest) { v.Revision = 0 }, func(v *OpenRequest) { v.RequestedBytes = MaxLeaseBytes + 1 }} {
		v := valid
		mutate(&v)
		if err := validateOpenRequest(v); err == nil {
			t.Fatalf("accepted %+v", v)
		}
	}
}

func TestPostgresMeteredConnectionAdmission(t *testing.T) {
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
	nextID := func() string {
		v, e := id.NewV7()
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	suffix := nextID()
	add := func(q string, args ...any) string {
		var v string
		if e := pool.QueryRow(ctx, q, args...).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	exec := func(q string, args ...any) {
		if _, e := pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	owner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, suffix+"@admission.invalid")
	group := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,'Admission','JP') RETURNING id::text`, suffix)
	node := add(`INSERT INTO nodes(id,group_id,name,region,host,capabilities,proxy_port,multiplier_milli) VALUES(gen_random_uuid(),$1,'Admission','JP','8.8.8.8',ARRAY['forward','proxy'],443,500) RETURNING id::text`, group)
	agent := add(`INSERT INTO agents(id,node_id,status,last_seen_at,desired_revision,applied_revision,capabilities) VALUES(gen_random_uuid(),$1,'online',clock_timestamp(),1,1,ARRAY['forward','proxy']) RETURNING id::text`, node)
	line := add(`INSERT INTO lines(id,name,created_by,multiplier_milli) VALUES(gen_random_uuid(),$1,$2,2000) RETURNING id::text`, suffix, owner)
	exec(`INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'egress')`, line, node)
	secondLine := add(`INSERT INTO lines(id,name,created_by,multiplier_milli) VALUES(gen_random_uuid(),$1,$2,2000) RETURNING id::text`, nextID(), owner)
	exec(`INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'egress')`, secondLine, node)
	plan := add(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),$1,1200) RETURNING id::text`, suffix)
	exec(`INSERT INTO plan_limits(plan_id,max_forward_rules_per_node) VALUES($1,4)`, plan)
	snapshot, _ := json.Marshal(map[string]any{"plan_name": "Admission", "quota_bytes": 1200, "default_multiplier_milli": 1000, "resource_group_ids": []string{group}, "line_ids": []string{line, secondLine}, "limits": map[string]any{"max_forward_rules_per_node": 4, "max_hops": 1}})
	member := add(`INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,clock_timestamp()-interval '1 hour',clock_timestamp()+interval '1 day','active',1,'UTC',$3) RETURNING id::text`, owner, plan, snapshot)
	policy := add(`INSERT INTO forward_target_policies(id,kind,protocol,port_start,port_end) VALUES(gen_random_uuid(),'public_host','TCP',443,443) RETURNING id::text`)
	rule := add(`INSERT INTO forward_rules(id,user_id,name,ingress_node_id,ingress_port,target_host,target_port,protocol) VALUES(gen_random_uuid(),$1,'Forward',$2,24000,'8.8.8.8',443,'TCP') RETURNING id::text`, owner, node)
	access := add(`INSERT INTO proxy_accesses(id,user_id,line_id,name,credential_hash,credential_ciphertext) VALUES(gen_random_uuid(),$1,$2,'Proxy',$3,$4) RETURNING id::text`, owner, line, strings.Repeat("a", 56), strings.Repeat("b", 64))
	var membershipEnd time.Time
	if err := pool.QueryRow(ctx, `SELECT ends_at FROM memberships WHERE id=$1`, member).Scan(&membershipEnd); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"forward_config": []map[string]any{{"id": rule, "ingress_port": 24000, "target_host": "8.8.8.8", "target_port": 443, "protocol": "TCP", "enabled": true}}, "proxy_config": []map[string]any{{"id": access, "user_id": owner, "line_id": line, "credential_hash": strings.Repeat("a", 56), "ingress_port": 443, "expires_at": membershipEnd}}})
	exec(`INSERT INTO config_revisions(node_id,revision,sha256,payload_json,status,applied_at) VALUES($1,1,$2,$3,'applied',clock_timestamp())`, node, strings.Repeat("a", 64), payload)
	repo := NewPostgresRepository(pool)
	if _, err := repo.activateCurrentPeriod(ctx, member); err != nil {
		t.Fatal(err)
	}
	request := OpenRequest{ConnectionID: nextID(), RequestID: nextID(), ResourceKind: "forward", ResourceID: rule, Revision: 1, RequestedBytes: 200}
	var wg sync.WaitGroup
	results := make(chan AdmissionGrant, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); v, e := repo.OpenConnection(ctx, node, request); results <- v; failures <- e }()
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatalf("concurrent open: %v", e)
		}
	}
	var grant AdmissionGrant
	for got := range results {
		if grant.Lease.ID != "" && grant.Lease.ID != got.Lease.ID {
			t.Fatal("duplicate lease")
		}
		grant = got
	}
	if grant.MultiplierMilli != 500 || grant.ConnectionID != request.ConnectionID || grant.Lease.GrantedBytes != 200 || grant.Lease.AgentID != agent {
		t.Fatalf("grant=%+v", grant)
	}
	var admittedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT started_at FROM usage_sessions WHERE id=$1`, request.ConnectionID).Scan(&admittedAt); err != nil || !admittedAt.Equal(grant.Lease.IssuedAt) {
		t.Fatalf("session admission time=%v lease issued=%v err=%v", admittedAt, grant.Lease.IssuedAt, err)
	}
	var reserved int64
	if err := pool.QueryRow(ctx, `SELECT reserved_bytes FROM billing_periods WHERE id=$1`, grant.Lease.PeriodID).Scan(&reserved); err != nil || reserved != 200 {
		t.Fatalf("reservation=%d %v", reserved, err)
	}
	// Authorization must compare the locked resource owner with the frozen
	// period owner, even if ownership changes between the initial lookup and
	// the locked resource query.
	otherOwner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, nextID()+"@admission.invalid")
	ownerTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerTx.Rollback(ctx)
	if _, err := ownerTx.Exec(ctx, `UPDATE forward_rules SET user_id=$2 WHERE id=$1`, rule, otherOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeResource(ctx, ownerTx, node, agent, grant.Lease.PeriodID, request, "", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("transferred rule authorized against prior owner's period: %v", err)
	}
	if err := ownerTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	staleTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer staleTx.Rollback(ctx)
	if _, err := staleTx.Exec(ctx, `UPDATE agents SET last_seen_at=clock_timestamp()-interval '50 seconds' WHERE id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeResource(ctx, staleTx, node, agent, grant.Lease.PeriodID, request, "", time.Now().Add(-time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale heartbeat accepted while authorization waited: %v", err)
	}
	if err := staleTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	conflict := request
	conflict.ResourceKind = "proxy"
	conflict.ResourceID = access
	if _, err := repo.OpenConnection(ctx, node, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("resource rebind=%v", err)
	}
	conflict = request
	conflict.ConnectionID = nextID()
	if _, err := repo.OpenConnection(ctx, node, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("request reused=%v", err)
	}
	for name, mutate := range map[string]func(*OpenRequest){"stale revision": func(v *OpenRequest) { v.Revision = 2 }, "unknown resource": func(v *OpenRequest) { v.ResourceID = nextID() }} {
		t.Run(name, func(t *testing.T) {
			v := request
			v.ConnectionID = nextID()
			v.RequestID = nextID()
			mutate(&v)
			if _, err := repo.OpenConnection(ctx, node, v); !errors.Is(err, ErrNotFound) {
				t.Fatalf("denied=%v", err)
			}
		})
	}
	if _, err := repo.OpenConnection(ctx, nextID(), request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other node=%v", err)
	}
	// Proxy multiplier and owner must come from the allowed resource, not the node default.
	proxyReq := OpenRequest{ConnectionID: nextID(), RequestID: nextID(), ResourceKind: "proxy", ResourceID: access, Revision: 1, RequestedBytes: 200}
	proxy, err := repo.OpenConnection(ctx, node, proxyReq)
	if err != nil || proxy.MultiplierMilli != 2000 {
		t.Fatalf("proxy grant=%+v %v", proxy, err)
	}
	// Changed execution target and policy revocation deny new connections.
	fresh := func() OpenRequest { v := request; v.RequestID = nextID(); v.ConnectionID = nextID(); return v }
	exec(`UPDATE forward_rules SET target_port=8443 WHERE id=$1`, rule)
	if _, err := repo.OpenConnection(ctx, node, fresh()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("changed target=%v", err)
	}
	exec(`UPDATE forward_rules SET target_port=443 WHERE id=$1`, rule)
	exec(`UPDATE forward_target_policies SET enabled=false WHERE id=$1`, policy)
	if _, err := repo.OpenConnection(ctx, node, fresh()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked target policy=%v", err)
	}
	exec(`UPDATE forward_target_policies SET enabled=true WHERE id=$1`, policy)
	renewRequest := RenewRequest{ConnectionID: request.ConnectionID, RequestID: nextID(), Revision: 1, RequestedBytes: 100}
	renewal, err := repo.RenewConnectionLease(ctx, node, renewRequest)
	if err != nil || renewal.MultiplierMilli != 500 {
		t.Fatalf("renew=%+v %v", renewal, err)
	}
	retried, err := repo.RenewConnectionLease(ctx, node, renewRequest)
	if err != nil || retried.Lease.ID != renewal.Lease.ID {
		t.Fatalf("unused renewal retry=%+v %v", retried, err)
	}
	changedRenew := renewRequest
	changedRenew.Revision = 2
	if _, err := repo.RenewConnectionLease(ctx, node, changedRenew); !errors.Is(err, ErrConflict) {
		t.Fatalf("renewal request ID reused with another revision=%v", err)
	}
	wrongRenew := renewRequest
	wrongRenew.ConnectionID = proxyReq.ConnectionID
	if _, err := repo.RenewConnectionLease(ctx, node, wrongRenew); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross connection renewal retry=%v", err)
	}
	exec(`INSERT INTO config_revisions(node_id,revision,sha256,payload_json,status,applied_at) VALUES($1,2,$2,$3,'applied',clock_timestamp())`, node, strings.Repeat("c", 64), payload)
	exec(`UPDATE agents SET desired_revision=2,applied_revision=2 WHERE id=$1`, agent)
	advanced, err := repo.RenewConnectionLease(ctx, node, RenewRequest{ConnectionID: request.ConnectionID, RequestID: nextID(), Revision: 2, RequestedBytes: 100})
	if err != nil || advanced.MultiplierMilli != grant.MultiplierMilli {
		t.Fatalf("renew after harmless config revision=%+v %v", advanced, err)
	}
	exec(`UPDATE proxy_accesses SET line_id=$2 WHERE id=$1`, access, secondLine)
	revision3, _ := json.Marshal(map[string]any{"forward_config": []map[string]any{{"id": rule, "ingress_port": 24000, "target_host": "8.8.8.8", "target_port": 443, "protocol": "TCP", "enabled": true}}, "proxy_config": []map[string]any{{"id": access, "user_id": owner, "line_id": secondLine, "credential_hash": strings.Repeat("a", 56), "ingress_port": 443, "expires_at": membershipEnd}}})
	exec(`INSERT INTO config_revisions(node_id,revision,sha256,payload_json,status,applied_at) VALUES($1,3,$2,$3,'applied',clock_timestamp())`, node, strings.Repeat("d", 64), revision3)
	exec(`UPDATE agents SET desired_revision=3,applied_revision=3 WHERE id=$1`, agent)
	if _, err := repo.RenewConnectionLease(ctx, node, RenewRequest{ConnectionID: proxyReq.ConnectionID, RequestID: nextID(), Revision: 3, RequestedBytes: 100}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renew moved proxy line=%v", err)
	}
	exec(`UPDATE users SET status='disabled' WHERE id=$1`, owner)
	if _, err := repo.OpenConnection(ctx, node, fresh()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled owner=%v", err)
	}
	if _, err := repo.RenewConnectionLease(ctx, node, RenewRequest{ConnectionID: request.ConnectionID, RequestID: nextID(), Revision: 2, RequestedBytes: 100}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renew disabled=%v", err)
	}
	// Historical report and settlement do not reauthorize mutable resources.
	exec(`DELETE FROM forward_rules WHERE id=$1`, rule)
	report := UsageReport{ConnectionID: request.ConnectionID, LeaseID: grant.Lease.ID, Sequence: 1, Counters: Counters{UploadedBytes: 10}, ObservedAt: grant.Lease.IssuedAt.Add(time.Microsecond)}
	if _, err := repo.RecordUsage(ctx, agent, report); err != nil {
		t.Fatalf("late usage=%v", err)
	}
	var proxyStarted time.Time
	if err := pool.QueryRow(ctx, `SELECT started_at FROM usage_sessions WHERE id=$1`, proxyReq.ConnectionID).Scan(&proxyStarted); err != nil {
		t.Fatal(err)
	}
	report.ConnectionID = proxyReq.ConnectionID
	report.ObservedAt = proxyStarted.Add(time.Microsecond)
	if _, err := repo.RecordUsage(ctx, agent, report); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross connection lease=%v", err)
	}
	if _, err := repo.SettleConnectionLease(ctx, node, request.ConnectionID, renewal.Lease.ID, 0); err != nil {
		t.Fatalf("unused renewal settlement=%v", err)
	}
	if _, err := repo.SettleConnectionLease(ctx, node, proxyReq.ConnectionID, grant.Lease.ID, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross connection settlement=%v", err)
	}
	if _, err := repo.SettleConnectionLease(ctx, node, request.ConnectionID, grant.Lease.ID, 5); err != nil {
		t.Fatalf("settle=%v", err)
	}
	if _, err := repo.SettleConnectionLease(ctx, node, request.ConnectionID, grant.Lease.ID, 5); err != nil {
		t.Fatalf("settle retry=%v", err)
	}
	t.Log(fmt.Sprintf("frozen period %s, multiplier %d", grant.Lease.PeriodID, grant.MultiplierMilli))
}
