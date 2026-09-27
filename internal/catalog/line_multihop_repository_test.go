package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMultiHopLineTopologyIsStoredButNotExecutable(t *testing.T) {
	databaseURL := catalogTestDatabaseURL()
	if databaseURL == "" {
		t.Skip("test PostgreSQL database is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	suffix := strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	insertID := func(query string, args ...any) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	actor := insertID(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "multi-admin-"+suffix+"@example.invalid")
	member := insertID(`INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "multi-user-"+suffix+"@example.invalid")
	group := insertID(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,$1,'JP') RETURNING id::text`, "MULTI."+suffix)
	otherGroup := insertID(`INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,$1,'US') RETURNING id::text`, "OTHER."+suffix)
	ingress := insertID(`INSERT INTO nodes(id,group_id,name,region,host,proxy_port,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Ingress','JP',$2,443,24441,ARRAY['proxy','forward']) RETURNING id::text`, group, "ingress-"+suffix+".example.invalid")
	relay := insertID(`INSERT INTO nodes(id,group_id,name,region,host,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Relay','JP',$2,24442,ARRAY['forward']) RETURNING id::text`, group, "relay-"+suffix+".example.invalid")
	egress := insertID(`INSERT INTO nodes(id,group_id,name,region,host,relay_port,capabilities) VALUES(gen_random_uuid(),$1,'Egress','JP',$2,24443,ARRAY['forward']) RETURNING id::text`, group, "egress-"+suffix+".example.invalid")
	plan := insertID(`INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),$1,1000000) RETURNING id::text`, "multi-plan-"+suffix)
	snapshot, _ := json.Marshal(map[string]any{"resource_group_ids": []string{group}, "limits": map[string]any{"allow_custom_lines": true, "max_custom_lines": 2, "max_hops": 3}})
	_, err = pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES (gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, member, plan, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE actor_user_id=ANY($1::uuid[])`, []string{actor, member})
		_, _ = pool.Exec(context.Background(), `DELETE FROM lines WHERE created_by=ANY($1::uuid[])`, []string{actor, member})
		_, _ = pool.Exec(context.Background(), `DELETE FROM memberships WHERE user_id=$1`, member)
		_, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE id=$1`, plan)
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id=ANY($1::uuid[])`, []string{ingress, relay, egress})
		_, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE id=$1`, group)
		_, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE id=$1`, otherGroup)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::uuid[])`, []string{actor, member})
	})
	repo := NewPostgresRepository(pool)
	hops := []LineHop{{Position: 0, NodeID: ingress, Role: "ingress"}, {Position: 1, NodeID: relay, Role: "relay"}, {Position: 2, NodeID: egress, Role: "egress"}}
	owned, err := repo.CreateCustomLine(ctx, LineInput{Name: "Owned multi " + suffix, Hops: hops, Enabled: false, Priority: 10, Weight: 2, Tags: []string{}}, member, "multi-create")
	if err != nil || len(owned.Hops) != 3 {
		t.Fatalf("create multi-hop = %+v, %v", owned, err)
	}
	loaded, err := repo.GetAllowedLine(ctx, member, owned.ID)
	if err != nil || len(loaded.Hops) != 3 || loaded.Hops[1].NodeID != relay {
		t.Fatalf("stored topology = %+v, %v", loaded, err)
	}
	if _, err := repo.GetUsableLine(ctx, member, owned.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled multi-hop became usable: %v", err)
	}
	if enabled, err := repo.UpdateOwnLine(ctx, member, owned.ID, LinePatch{Enabled: boolPtr(true)}, "multi-enable"); err != nil || !enabled.Enabled {
		t.Fatalf("enabled multi-hop: %+v, %v", enabled, err)
	}
	if _, err := repo.GetUsableLine(ctx, member, owned.ID); err != nil {
		t.Fatalf("enabled multi-hop not usable: %v", err)
	}
	twoHops := []LineHop{hops[0], {Position: 1, NodeID: egress, Role: "egress"}}
	shared, err := repo.CreateSharedLine(ctx, LineInput{Name: "Shared multi " + suffix, Hops: twoHops, Enabled: false, Priority: 20, Weight: 1, Tags: []string{}}, actor, "shared-multi")
	if err != nil || len(shared.Hops) != 2 {
		t.Fatalf("shared multi-hop = %+v, %v", shared, err)
	}
	all, err := repo.ListAllLines(ctx, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range all {
		if line.ID == shared.ID && len(line.Hops) == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("shared multi-hop missing from administrator directory")
	}
	for _, mutation := range []struct{ apply, restore string }{
		{`UPDATE nodes SET enabled=false WHERE id=$1`, `UPDATE nodes SET enabled=true WHERE id=$1`},
		{`UPDATE nodes SET capabilities=ARRAY['proxy'],relay_port=NULL WHERE id=$1`, `UPDATE nodes SET capabilities=ARRAY['forward'],relay_port=24442 WHERE id=$1`},
	} {
		if _, err := pool.Exec(ctx, mutation.apply, relay); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateCustomLine(ctx, LineInput{Name: "Invalid relay " + suffix, Hops: hops, Enabled: false, Priority: 10, Weight: 1}, member, "invalid-relay"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid relay allowed: %v", err)
		}
		if _, err := pool.Exec(ctx, mutation.restore, relay); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET group_id=$2 WHERE id=$1`, relay, otherGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateCustomLine(ctx, LineInput{Name: "Unauthorized relay " + suffix, Hops: hops, Enabled: false, Priority: 10, Weight: 1}, member, "ungranted-relay"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungranted middle group allowed: %v", err)
	}
	if _, err := repo.GetAllowedLine(ctx, member, owned.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungranted middle group exposed: %v", err)
	}
	if _, err := repo.UpdateOwnLine(ctx, member, owned.ID, LinePatch{Name: stringPtr("Unauthorized edit")}, "ungranted-edit"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungranted middle group edited: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET group_id=$2 WHERE id=$1`, relay, group); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET snapshot_json=jsonb_set(snapshot_json,'{limits,max_hops}','2') WHERE user_id=$1`, member); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateCustomLine(ctx, LineInput{Name: "Over max " + suffix, Hops: hops, Enabled: false, Priority: 10, Weight: 1}, member, "max-hops"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("over-limit multi-hop = %v", err)
	}
	if _, err := repo.CreateCustomLine(ctx, LineInput{Name: "Within max " + suffix, Hops: twoHops, Enabled: false, Priority: 10, Weight: 1}, member, "two-hops"); err != nil {
		t.Fatalf("max_hops=2 rejected two hops: %v", err)
	}
	if err := repo.DeleteOwnLine(ctx, member, owned.ID, "delete-topology"); err != nil {
		t.Fatalf("delete owned topology after allowance change: %v", err)
	}
}
