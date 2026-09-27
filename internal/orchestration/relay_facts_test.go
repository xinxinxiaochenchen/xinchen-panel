package orchestration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRelayFactsRequireAppliedCurrentGeneration(t *testing.T) {
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
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	insert := func(query string, args ...any) string {
		t.Helper()
		var value string
		if err := tx.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%x", nonce)
	owner := insert(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "relay-facts-"+suffix+"@example.invalid")
	group := insert(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,'Relay facts','US') RETURNING id::text`, "TEST.RELAY."+suffix)
	ingress := insert(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Ingress','US','in.example.com',24441,24442,ARRAY['proxy','forward']) RETURNING id::text`, group)
	egress := insert(`INSERT INTO nodes(id,group_id,name,region,host,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Egress','US','out.example.com',24443,ARRAY['forward']) RETURNING id::text`, group)
	line := insert(`INSERT INTO lines(id,name,created_by) VALUES(gen_random_uuid(),'Relay facts',$1) RETURNING id::text`, owner)
	if _, err := tx.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'ingress'),($1,1,$3,'egress')`, line, ingress, egress); err != nil {
		t.Fatal(err)
	}
	plan := insert(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),$1,1000000) RETURNING id::text`, "relay-plan-"+suffix)
	grant, err := json.Marshal(map[string]any{"resource_group_ids": []string{group}, "line_ids": []string{line}, "limits": map[string]any{"max_hops": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES(gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 hour','active',1,'UTC',$3)`, owner, plan, grant); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO proxy_accesses(id,user_id,line_id,name,credential_hash,credential_ciphertext)
VALUES(gen_random_uuid(),$1,$2,'Relay access',$3,$4)`, owner, line, strings.Repeat("a", 56), []byte(strings.Repeat("x", 40))); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{ingress, egress} {
		fingerprint := strings.Repeat("a", 64)
		if node == egress {
			fingerprint = strings.Repeat("b", 64)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agents(id,node_id,cert_fingerprint,cert_expires_at,status,last_seen_at,capabilities)
VALUES(gen_random_uuid(),$1,$2,now()+interval '1 hour','online',now(),ARRAY['forward','proxy','relay'])`, node, fingerprint); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_relay_certificate_grants(node_id,fingerprint,csr_digest,certificate_pem,host,expires_at)
VALUES($1,$2,$3,$4,$5,now()+interval '1 hour')`, node, strings.Repeat("c", 64), strings.Repeat("d", 64), []byte("test"), "relay.example.com"); err != nil {
			t.Fatal(err)
		}
	}
	read := func() []RelayLineFacts {
		t.Helper()
		facts, err := readRelayLineFacts(ctx, tx, ingress)
		if err != nil {
			t.Fatal(err)
		}
		if len(facts) != 1 || len(facts[0].Hops) != 2 {
			t.Fatalf("relay facts = %+v", facts)
		}
		return facts
	}
	if facts := read(); facts[0].Hops[1].AppliedRelay {
		t.Fatal("downstream without ACK was ready")
	}
	checkProxy := func(wantReady bool) {
		t.Helper()
		facts := read()
		relays, _, err := CompileRelaySnapshots(ctx, facts, &MemoryRelaySecretStore{})
		if err != nil {
			t.Fatal(err)
		}
		proxies, err := readProxyFacts(ctx, tx, ingress, facts, relays)
		if err != nil || len(proxies) != 1 || proxies[0].RelayReady != wantReady || proxies[0].HopCount != 2 || proxies[0].MaxHops != 2 {
			t.Fatalf("proxy readiness = %+v, error=%v", proxies, err)
		}
	}
	checkProxy(false)
	if _, err := tx.Exec(ctx, `INSERT INTO config_revisions(node_id,revision,sha256,payload_json,status,applied_at)
VALUES($1,1,$2,jsonb_build_object('relay_config',jsonb_build_array(jsonb_build_object('line_id',$3::text,'generation',1,'role','egress'))),'applied',now())`, egress, strings.Repeat("e", 64), line); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET desired_revision=1,applied_revision=1 WHERE node_id=$1`, egress); err != nil {
		t.Fatal(err)
	}
	if facts := read(); !facts[0].Hops[1].AppliedRelay {
		t.Fatal("current generation ACK was not recognized")
	}
	checkProxy(true)
	if _, err := tx.Exec(ctx, `UPDATE agents SET desired_revision=2 WHERE node_id=$1`, egress); err != nil {
		t.Fatal(err)
	}
	if facts := read(); facts[0].Hops[1].AppliedRelay {
		t.Fatal("stale ACK was recognized")
	}
	checkProxy(false)
}
