package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMultiHopAdmissionRequiresCurrentRouteAndChargesIngressOnce(t *testing.T) {
	dsn := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	add := func(query string, args ...any) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	newID := func() string {
		value, err := id.NewV7()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	owner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "bill-multi-"+suffix+"@example.invalid")
	group := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,$1,'JP') RETURNING id::text`, "BILL.MULTI."+suffix)
	egressGroup := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,$1,'JP') RETURNING id::text`, "BILL.MULTI.EGRESS."+suffix)
	ingress := add(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Ingress','JP',$2,443,24441,ARRAY['proxy','forward']) RETURNING id::text`, group, "bill-in-"+suffix+".example.invalid")
	egress := add(`INSERT INTO nodes(id,group_id,name,region,host,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Egress','JP',$2,24442,ARRAY['forward']) RETURNING id::text`, egressGroup, "bill-out-"+suffix+".example.invalid")
	line := add(`INSERT INTO lines(id,name,owner_user_id,created_by,multiplier_milli) VALUES(gen_random_uuid(),$1,$2,$2,2000) RETURNING id::text`, "Bill multi "+suffix, owner)
	exec(`INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'ingress'),($1,1,$3,'egress')`, line, ingress, egress)
	plan := add(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),$1,1000000) RETURNING id::text`, "Bill multi plan "+suffix)
	snapshot, _ := json.Marshal(map[string]any{"plan_name": "Bill multi", "quota_bytes": 1000000, "default_multiplier_milli": 1000, "resource_group_ids": []string{group, egressGroup}, "line_ids": []string{}, "limits": map[string]any{"allow_custom_lines": true, "max_hops": 2}})
	member := add(`INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,clock_timestamp()-interval '1 hour',clock_timestamp()+interval '1 day','active',1,'UTC',$3) RETURNING id::text`, owner, plan, snapshot)
	access := add(`INSERT INTO proxy_accesses(id,user_id,line_id,name,credential_hash,credential_ciphertext) VALUES(gen_random_uuid(),$1,$2,'Bill access',$3,$4) RETURNING id::text`, owner, line, strings.Repeat("a", 56), strings.Repeat("b", 64))
	for _, spec := range []struct{ node, fingerprint string }{{ingress, strings.Repeat("a", 64)}, {egress, strings.Repeat("b", 64)}} {
		exec(`INSERT INTO agents(id,node_id,status,last_seen_at,desired_revision,applied_revision,capabilities,cert_fingerprint,cert_expires_at) VALUES(gen_random_uuid(),$1,'online',clock_timestamp(),1,1,ARRAY['proxy','relay'],$2,clock_timestamp()+interval '1 day')`, spec.node, spec.fingerprint)
		exec(`INSERT INTO agent_relay_certificate_grants(node_id,fingerprint,csr_digest,certificate_pem,host,expires_at) VALUES($1,$2,$3,$4,'relay.example.invalid',clock_timestamp()+interval '1 day')`, spec.node, strings.Repeat("c", 64), strings.Repeat("d", 64), []byte("test"))
	}
	var memberEnd time.Time
	if err := pool.QueryRow(ctx, `SELECT ends_at FROM memberships WHERE id=$1`, member).Scan(&memberEnd); err != nil {
		t.Fatal(err)
	}
	ingressPayload, _ := json.Marshal(map[string]any{"proxy_config": []any{map[string]any{"id": access, "user_id": owner, "line_id": line, "credential_hash": strings.Repeat("a", 56), "ingress_port": 443, "relay_generation": 1, "expires_at": memberEnd}}, "relay_config": []any{map[string]any{"line_id": line, "generation": 1, "role": "ingress"}}})
	egressPayload, _ := json.Marshal(map[string]any{"relay_config": []any{map[string]any{"line_id": line, "generation": 1, "role": "egress"}}})
	exec(`INSERT INTO config_revisions(node_id,revision,sha256,payload_json,status,applied_at) VALUES($1,1,$2,$3,'applied',clock_timestamp())`, ingress, strings.Repeat("e", 64), ingressPayload)
	exec(`INSERT INTO config_revisions(node_id,revision,sha256,payload_json,status,applied_at) VALUES($1,1,$2,$3,'applied',clock_timestamp())`, egress, strings.Repeat("f", 64), egressPayload)
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM connection_lease_bindings WHERE connection_id IN (SELECT id FROM usage_sessions WHERE user_id=$1)`, owner)
		_, _ = pool.Exec(cleanup, `DELETE FROM usage_events WHERE user_id=$1`, owner)
		_, _ = pool.Exec(cleanup, `DELETE FROM usage_sessions WHERE user_id=$1`, owner)
		_, _ = pool.Exec(cleanup, `DELETE FROM quota_leases WHERE billing_period_id IN (SELECT id FROM billing_periods WHERE user_id=$1)`, owner)
		_, _ = pool.Exec(cleanup, `DELETE FROM billing_periods WHERE user_id=$1`, owner)
		_, _ = pool.Exec(cleanup, `DELETE FROM config_revisions WHERE node_id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(cleanup, `DELETE FROM agent_relay_certificate_grants WHERE node_id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(cleanup, `DELETE FROM agents WHERE node_id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(cleanup, `DELETE FROM proxy_accesses WHERE id=$1`, access)
		_, _ = pool.Exec(cleanup, `DELETE FROM outbox_events WHERE aggregate_id=$1`, member)
		_, _ = pool.Exec(cleanup, `DELETE FROM memberships WHERE id=$1`, member)
		_, _ = pool.Exec(cleanup, `DELETE FROM lines WHERE id=$1`, line)
		_, _ = pool.Exec(cleanup, `DELETE FROM plans WHERE id=$1`, plan)
		_, _ = pool.Exec(cleanup, `DELETE FROM nodes WHERE id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(cleanup, `DELETE FROM resource_groups WHERE id=$1`, group)
		_, _ = pool.Exec(cleanup, `DELETE FROM resource_groups WHERE id=$1`, egressGroup)
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id=$1`, owner)
	})
	repo := NewPostgresRepository(pool)
	if _, err := repo.activateCurrentPeriod(ctx, member); err != nil {
		t.Fatal(err)
	}
	request := func() OpenRequest {
		return OpenRequest{ConnectionID: newID(), RequestID: newID(), ResourceKind: "proxy", ResourceID: access, Revision: 1, RequestedBytes: 100}
	}
	exec(`UPDATE agents SET desired_revision=2 WHERE node_id=$1`, egress)
	if _, err := repo.OpenConnection(ctx, ingress, request()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale downstream ACK admitted: %v", err)
	}
	exec(`UPDATE agents SET desired_revision=1 WHERE node_id=$1`, egress)
	req := request()
	grant, err := repo.OpenConnection(ctx, ingress, req)
	if err != nil || grant.MultiplierMilli != 2000 {
		t.Fatalf("multi-hop admission = %+v %v", grant, err)
	}
	var frozenLine string
	var sessionCount int
	if err := pool.QueryRow(ctx, `SELECT line_id::text FROM usage_sessions WHERE id=$1`, req.ConnectionID).Scan(&frozenLine); err != nil || frozenLine != line {
		t.Fatalf("frozen line = %s %v", frozenLine, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM usage_sessions WHERE user_id=$1`, owner).Scan(&sessionCount); err != nil || sessionCount != 1 {
		t.Fatalf("logical ingress sessions = %d %v", sessionCount, err)
	}
	exec(`UPDATE config_revisions SET payload_json=jsonb_set(payload_json,'{relay_config,0,generation}','2') WHERE node_id=$1 AND revision=1`, egress)
	if _, err := repo.OpenConnection(ctx, ingress, request()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong downstream route generation admitted: %v", err)
	}
	exec(`UPDATE config_revisions SET payload_json=jsonb_set(payload_json,'{relay_config,0,generation}','1') WHERE node_id=$1 AND revision=1`, egress)
	if _, err := repo.OpenConnection(ctx, ingress, request()); err != nil {
		t.Fatalf("restored downstream route denied: %v", err)
	}
	exec(`UPDATE billing_periods SET snapshot_json=jsonb_set(snapshot_json,'{resource_group_ids}',to_jsonb(ARRAY[$2::text])) WHERE user_id=$1`, owner, group)
	if _, err := repo.OpenConnection(ctx, ingress, request()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unauthorized downstream resource group admitted: %v", err)
	}
	exec(`UPDATE billing_periods SET snapshot_json=jsonb_set(snapshot_json,'{resource_group_ids}',to_jsonb(ARRAY[$2::text,$3::text])) WHERE user_id=$1`, owner, group, egressGroup)
	if _, err := repo.OpenConnection(ctx, ingress, request()); err != nil {
		t.Fatalf("restored downstream group grant denied: %v", err)
	}
	exec(`UPDATE agents SET last_seen_at=clock_timestamp()-interval '50 seconds' WHERE node_id=$1`, egress)
	if _, err := repo.OpenConnection(ctx, ingress, request()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale egress heartbeat admitted: %v", err)
	}
	exec(`UPDATE agents SET last_seen_at=clock_timestamp() WHERE node_id=$1`, egress)
	exec(`UPDATE agent_relay_certificate_grants SET expires_at=clock_timestamp()-interval '1 second' WHERE node_id=$1`, egress)
	if _, err := repo.OpenConnection(ctx, ingress, request()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired egress relay certificate admitted: %v", err)
	}
	exec(`UPDATE agent_relay_certificate_grants SET expires_at=clock_timestamp()+interval '1 day' WHERE node_id=$1`, egress)
	exec(`UPDATE resource_groups SET enabled=false WHERE id=$1`, egressGroup)
	if _, err := repo.OpenConnection(ctx, ingress, request()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled resource group admitted: %v", err)
	}
	exec(`UPDATE resource_groups SET enabled=true WHERE id=$1`, egressGroup)
	// Disabling a group revokes relay grants through the database trigger. Rebuild
	// the downstream grant before testing the independent max_hops gate.
	exec(`INSERT INTO agent_relay_certificate_grants(node_id,fingerprint,csr_digest,certificate_pem,host,expires_at) VALUES($1,$2,$3,$4,'relay.example.invalid',clock_timestamp()+interval '1 day')`, egress, strings.Repeat("c", 64), strings.Repeat("d", 64), []byte("test"))
	if _, err := repo.OpenConnection(ctx, ingress, request()); err != nil {
		t.Fatalf("restored relay baseline denied: %v", err)
	}
	exec(`UPDATE billing_periods SET snapshot_json=jsonb_set(snapshot_json,'{limits,max_hops}','1') WHERE user_id=$1`, owner)
	if _, err := repo.OpenConnection(ctx, ingress, request()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("over-limit route admitted: %v", err)
	}
	exec(`UPDATE billing_periods SET snapshot_json=jsonb_set(snapshot_json,'{limits,max_hops}','2') WHERE user_id=$1`, owner)
	if _, err := repo.OpenConnection(ctx, ingress, request()); err != nil {
		t.Fatalf("restored hop limit baseline denied: %v", err)
	}
	exec(`UPDATE lines SET relay_generation=2 WHERE id=$1`, line)
	if _, err := repo.OpenConnection(ctx, ingress, request()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old relay generation admitted: %v", err)
	}
	if _, err := repo.RenewConnectionLease(ctx, ingress, RenewRequest{ConnectionID: req.ConnectionID, RequestID: newID(), Revision: 1, RequestedBytes: 100}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked generation renewed: %v", err)
	}
}
