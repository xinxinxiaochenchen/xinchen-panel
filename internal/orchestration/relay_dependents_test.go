package orchestration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRelayACKReconcilesIngress(t *testing.T) {
	url := orchestrationTestDatabaseURL()
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
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "relay-ack-"+suffix+"@example.invalid")
	group := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,$1,'US') RETURNING id::text`, "RELAY.ACK."+suffix)
	ingress := add(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Ingress','US',$2,443,24441,ARRAY['proxy','forward']) RETURNING id::text`, group, "ack-in-"+suffix+".example.invalid")
	egress := add(`INSERT INTO nodes(id,group_id,name,region,host,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Egress','US',$2,24442,ARRAY['forward']) RETURNING id::text`, group, "ack-out-"+suffix+".example.invalid")
	line := add(`INSERT INTO lines(id,name,created_by) VALUES(gen_random_uuid(),$1,$2) RETURNING id::text`, "ACK "+suffix, owner)
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'ingress'),($1,1,$3,'egress')`, line, ingress, egress); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{ingress, egress} {
		if _, err := pool.Exec(ctx, `INSERT INTO agents(id,node_id,status,version,last_seen_at,capabilities) VALUES(gen_random_uuid(),$1,'online','test',now(),ARRAY['forward','relay','proxy'])`, node); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM config_revisions WHERE node_id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(context.Background(), `DELETE FROM agents WHERE node_id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(context.Background(), `DELETE FROM lines WHERE id=$1`, line)
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE id=$1`, group)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, owner)
	})
	repo := NewRevisionRepository(pool)
	if err := repo.ReconcileRelayDependents(ctx, egress); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT desired_revision FROM agents WHERE node_id=$1`, ingress).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("ingress desired revision = %d, %v", revision, err)
	}
}
