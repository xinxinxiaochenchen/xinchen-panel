package subscription

import (
	"context"
	"controlplane/internal/proxyaccess"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMultiHopExportWaitsForAllRelayACKs(t *testing.T) {
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
		var value string
		if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	owner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "sub-multi-"+suffix+"@example.invalid")
	group := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,$1,'JP') RETURNING id::text`, "SUB.MULTI."+suffix)
	ingress := add(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Ingress','JP',$2,443,24441,ARRAY['proxy','forward']) RETURNING id::text`, group, "sub-in-"+suffix+".example.invalid")
	egress := add(`INSERT INTO nodes(id,group_id,name,region,host,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Egress','JP',$2,24442,ARRAY['forward']) RETURNING id::text`, group, "sub-out-"+suffix+".example.invalid")
	line := add(`INSERT INTO lines(id,name,owner_user_id,created_by) VALUES(gen_random_uuid(),'Multi',$1,$1) RETURNING id::text`, owner)
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'ingress'),($1,1,$3,'egress')`, line, ingress, egress); err != nil {
		t.Fatal(err)
	}
	plan := add(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),$1,1000000) RETURNING id::text`, "sub-multi-plan-"+suffix)
	snapshot, _ := json.Marshal(map[string]any{"resource_group_ids": []string{group}, "limits": map[string]any{"allow_custom_lines": true, "max_hops": 2, "max_subscriptions": 1}})
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, owner, plan, snapshot); err != nil {
		t.Fatal(err)
	}
	cipher, _ := proxyaccess.NewCredentialCipher(make([]byte, 32))
	proxies := proxyaccess.NewPostgresRepository(pool, cipher)
	access, password, err := proxies.Create(ctx, owner, proxyaccess.AccessInput{Name: "Multi", LineID: line, Enabled: true}, "multi-export")
	if err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool, cipher)
	sub, token, err := repo.Create(ctx, owner, Input{Name: "Multi", NameTemplate: "{name}", ProxyAccessIDs: []string{access.ID}, Enabled: true}, "multi-export")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM config_revisions WHERE node_id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(context.Background(), `DELETE FROM agents WHERE node_id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriptions WHERE id=$1`, sub.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM proxy_accesses WHERE id=$1`, access.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE actor_user_id=$1`, owner)
		_, _ = pool.Exec(context.Background(), `DELETE FROM memberships WHERE user_id=$1`, owner)
		_, _ = pool.Exec(context.Background(), `DELETE FROM lines WHERE id=$1`, line)
		_, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE id=$1`, plan)
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE id=$1`, group)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, owner)
	})
	for _, node := range []string{ingress, egress} {
		if _, err := pool.Exec(ctx, `INSERT INTO agents(id,node_id,status,version,last_seen_at,desired_revision,applied_revision) VALUES(gen_random_uuid(),$1,'online','test',now(),1,1)`, node); err != nil {
			t.Fatal(err)
		}
	}
	digest := proxyaccess.TrojanDigest(password)
	for _, spec := range []struct{ node, role string }{{ingress, "ingress"}, {egress, "egress"}} {
		payload, _ := json.Marshal(map[string]any{"relay_config": []any{map[string]any{"line_id": line, "generation": 1, "role": spec.role}}, "proxy_config": []any{}})
		if spec.role == "ingress" {
			payload, _ = json.Marshal(map[string]any{"relay_config": []any{map[string]any{"line_id": line, "generation": 1, "role": spec.role}}, "proxy_config": []any{map[string]any{"id": access.ID, "credential_hash": digest}}})
		}
		if _, err := pool.Exec(ctx, `INSERT INTO config_revisions(node_id,revision,sha256,payload_json,status,applied_at) VALUES($1,1,$2,$3,'applied',now())`, spec.node, strings.Repeat("a", 64), payload); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE proxy_accesses SET apply_status='active' WHERE id=$1`, access.ID); err != nil {
		t.Fatal(err)
	}
	if body, _, err := repo.Export(ctx, token, "mihomo"); err != nil || !strings.Contains(string(body), password) {
		t.Fatalf("ready route export = %v %s", err, body)
	}
	if _, err := pool.Exec(ctx, `UPDATE agents SET desired_revision=2 WHERE node_id=$1`, egress); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Export(ctx, token, "mihomo"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale downstream ACK exported: %v", err)
	}
}
