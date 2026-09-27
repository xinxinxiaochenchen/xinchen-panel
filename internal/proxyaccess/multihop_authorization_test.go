package proxyaccess

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresAuthorizeMultiHopProxyLine(t *testing.T) {
	url := proxyTestDatabaseURL()
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
	owner := add(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "proxy-multi-"+suffix+"@example.invalid")
	group := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,$1,'JP') RETURNING id::text`, "PROXY.MULTI."+suffix)
	otherGroup := add(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,$1,'US') RETURNING id::text`, "PROXY.OTHER."+suffix)
	ingress := add(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Ingress','JP',$2,443,24441,ARRAY['proxy','forward']) RETURNING id::text`, group, "in-"+suffix+".example.invalid")
	egress := add(`INSERT INTO nodes(id,group_id,name,region,host,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Egress','JP',$2,24442,ARRAY['forward']) RETURNING id::text`, group, "out-"+suffix+".example.invalid")
	line := add(`INSERT INTO lines(id,name,owner_user_id,created_by) VALUES(gen_random_uuid(),'Multi',$1,$1) RETURNING id::text`, owner)
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'ingress'),($1,1,$3,'egress')`, line, ingress, egress); err != nil {
		t.Fatal(err)
	}
	plan := add(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),$1,1000000) RETURNING id::text`, "proxy-multi-plan-"+suffix)
	snapshot, _ := json.Marshal(map[string]any{"resource_group_ids": []string{group}, "limits": map[string]any{"allow_custom_lines": true, "max_hops": 2}})
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, owner, plan, snapshot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM memberships WHERE user_id=$1`, owner)
		_, _ = pool.Exec(context.Background(), `DELETE FROM lines WHERE id=$1`, line)
		_, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE id=$1`, plan)
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id=ANY($1::uuid[])`, []string{ingress, egress})
		_, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE id=ANY($1::uuid[])`, []string{group, otherGroup})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, owner)
	})
	check := func(want error) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		_, err = authorizeProxyLine(ctx, tx, owner, line)
		if want == nil && err != nil || want != nil && !errors.Is(err, want) {
			t.Fatalf("authorization = %v, want %v", err, want)
		}
	}
	check(nil)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET group_id=$2 WHERE id=$1`, egress, otherGroup); err != nil {
		t.Fatal(err)
	}
	check(ErrNotFound)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET group_id=$2 WHERE id=$1`, egress, group); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET relay_port=NULL WHERE id=$1`, egress); err != nil {
		t.Fatal(err)
	}
	check(ErrNotFound)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET relay_port=24442 WHERE id=$1`, egress); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json=jsonb_set(snapshot_json,'{limits,max_hops}','1') WHERE user_id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	check(ErrNotFound)
}
