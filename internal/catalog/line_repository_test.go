package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresSingleHopLinesRespectCurrentEntitlementAndNodeState(t *testing.T) {
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
	var actorID, memberID, groupID, otherGroupID, proxyID, forwardID, otherProxyID, planID string
	for i, email := range []string{"line-admin@example.invalid", "line-member@example.invalid"} {
		var userID string
		if err := pool.QueryRow(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES (gen_random_uuid(),$1,'hash','active') RETURNING id::text`, email).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			actorID = userID
		} else {
			memberID = userID
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE actor_user_id=$1 OR actor_user_id=$2`, actorID, memberID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM memberships WHERE user_id=$1`, memberID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM lines WHERE created_by=$1 OR owner_user_id=$2`, actorID, memberID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE id=$1`, planID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id=ANY($1::uuid[])`, []string{proxyID, forwardID, otherProxyID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE id=$1 OR id=$2`, groupID, otherGroupID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1 OR id=$2`, actorID, memberID)
	})
	for i, code := range []string{"TEST.LINE1", "TEST.LINE2"} {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region) VALUES (gen_random_uuid(),$1,$1,'JP') RETURNING id::text`, code).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			groupID = id
		} else {
			otherGroupID = id
		}
	}
	for i, spec := range []struct{ groupID, name, capability string }{
		{groupID, "Line Proxy", "proxy"}, {groupID, "Line Forward", "forward"}, {otherGroupID, "Line Other", "proxy"},
	} {
		var nodeID string
		var proxyPort *int
		if spec.capability == "proxy" {
			port := 443
			proxyPort = &port
		}
		if err := pool.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities)
VALUES (gen_random_uuid(),$1,$2,'JP',$3,$4,ARRAY[$5]::text[]) RETURNING id::text`,
			spec.groupID, spec.name, []string{"line-proxy.example.invalid", "line-forward.example.invalid", "line-other.example.invalid"}[i], proxyPort, spec.capability).Scan(&nodeID); err != nil {
			t.Fatal(err)
		}
		switch i {
		case 0:
			proxyID = nodeID
		case 1:
			forwardID = nodeID
		case 2:
			otherProxyID = nodeID
		}
	}
	repo := NewPostgresRepository(pool)
	shared, err := repo.CreateSharedLine(ctx, LineInput{Name: "Shared JP", NodeID: proxyID, Enabled: true, Priority: 100, Weight: 1, Tags: []string{}}, actorID, "shared-line")
	if err != nil || shared.OwnerUserID != nil || len(shared.Hops) != 1 || shared.Hops[0].Role != "egress" || shared.Hops[0].NodeID != proxyID {
		t.Fatalf("shared line = %+v, %v", shared, err)
	}
	if _, err := repo.CreateSharedLine(ctx, LineInput{Name: "Shared JP", NodeID: proxyID, Enabled: true, Priority: 100, Weight: 1, Tags: []string{}}, actorID, "duplicate-line"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate shared line = %v", err)
	}
	if _, err := repo.CreateSharedLine(ctx, LineInput{Name: "Bad Forward", NodeID: forwardID, Enabled: true, Priority: 100, Weight: 1, Tags: []string{}}, actorID, "bad-forward"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forward-only egress = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=false WHERE id=$1`, proxyID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateSharedLine(ctx, LineInput{Name: "Disabled Node", NodeID: proxyID, Enabled: true, Priority: 100, Weight: 1, Tags: []string{}}, actorID, "disabled-node"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled proxy egress = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET enabled=true WHERE id=$1`, proxyID); err != nil {
		t.Fatal(err)
	}
	memberInput := LineInput{Name: "Own JP", NodeID: proxyID, Enabled: true, Priority: 100, Weight: 1, Tags: []string{}}
	if _, err := repo.CreateCustomLine(ctx, memberInput, memberID, "no-membership"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("custom line without membership = %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO plans(id,name,quota_bytes) VALUES (gen_random_uuid(),'line-integration-plan',1000000) RETURNING id::text`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(map[string]any{"resource_group_ids": []string{groupID}, "line_ids": []string{shared.ID},
		"limits": map[string]any{"allow_custom_lines": true, "max_custom_lines": 1, "max_hops": 1}})
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json)
VALUES (gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',$3)`, memberID, planID, snapshot); err != nil {
		t.Fatal(err)
	}
	owned, err := repo.CreateCustomLine(ctx, memberInput, memberID, "own-line")
	if err != nil || owned.OwnerUserID == nil || *owned.OwnerUserID != memberID {
		t.Fatalf("custom line = %+v, %v", owned, err)
	}
	if _, err := repo.CreateCustomLine(ctx, LineInput{Name: "Own Second", NodeID: proxyID, Enabled: true, Priority: 100, Weight: 1, Tags: []string{}}, memberID, "limit-line"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("custom line beyond limit = %v", err)
	}
	if _, err := repo.CreateCustomLine(ctx, LineInput{Name: "Other Group", NodeID: otherProxyID, Enabled: true, Priority: 100, Weight: 1, Tags: []string{}}, memberID, "other-group"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungranted group = %v", err)
	}
	allowed, err := repo.ListAllowedLines(ctx, memberID, 10, "")
	if err != nil || len(allowed) != 2 || allowed[0].ID == allowed[1].ID {
		t.Fatalf("allowed lines = %+v, %v", allowed, err)
	}
	if _, err := repo.GetAllowedLine(ctx, memberID, shared.ID); err != nil {
		t.Fatalf("granted shared line = %v", err)
	}
	if administrative, err := repo.GetLine(ctx, shared.ID); err != nil || administrative.ID != shared.ID {
		t.Fatalf("admin line detail = %+v, %v", administrative, err)
	}
	all, err := repo.ListAllLines(ctx, 10, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("all lines = %+v, %v", all, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lines SET enabled=false WHERE id=$1`, owned.ID); err != nil {
		t.Fatal(err)
	}
	if disabled, err := repo.GetAllowedLine(ctx, memberID, owned.ID); err != nil || disabled.Enabled {
		t.Fatalf("disabled own line for management = %+v, %v", disabled, err)
	}
	managed, err := repo.ListAllowedLines(ctx, memberID, 10, "")
	if err != nil || len(managed) != 2 {
		t.Fatalf("disabled own line missing from list = %+v, %v", managed, err)
	}
	if _, err := repo.GetUsableLine(ctx, memberID, owned.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled own line usable for traffic: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lines SET enabled=true WHERE id=$1`, owned.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES ($1,1,$2,'relay')`, shared.ID, forwardID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetAllowedLine(ctx, memberID, shared.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("multi-hop line misreported as single-hop: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM line_hops WHERE line_id=$1 AND position=1`, shared.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE resource_groups SET enabled=false WHERE id=$1`, groupID); err != nil {
		t.Fatal(err)
	}
	allowed, err = repo.ListAllowedLines(ctx, memberID, 10, "")
	if err != nil || len(allowed) != 1 || allowed[0].ID != owned.ID {
		t.Fatalf("disabled group line management = %+v, %v", allowed, err)
	}
	if _, err := repo.GetUsableLine(ctx, memberID, owned.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled group line usable for traffic: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE resource_groups SET enabled=true WHERE id=$1`, groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM lines WHERE id=$1`, owned.ID); err != nil {
		t.Fatal(err)
	}
	var start sync.WaitGroup
	start.Add(1)
	results := make(chan error, 2)
	for _, name := range []string{"Parallel One", "Parallel Two"} {
		go func(name string) {
			start.Wait()
			_, err := repo.CreateCustomLine(ctx, LineInput{Name: name, NodeID: proxyID, Enabled: true, Priority: 100, Weight: 1, Tags: []string{}}, memberID, name)
			results <- err
		}(name)
	}
	start.Done()
	var created, limited int
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrLimitReached):
			limited++
		default:
			t.Fatalf("concurrent create = %v", err)
		}
	}
	if created != 1 || limited != 1 {
		t.Fatalf("concurrent custom lines created=%d limited=%d", created, limited)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET capabilities=ARRAY['forward']::text[],proxy_port=NULL WHERE id=$1`, proxyID); err != nil {
		t.Fatal(err)
	}
	allowed, err = repo.ListAllowedLines(ctx, memberID, 10, "")
	if err != nil || len(allowed) != 1 || allowed[0].OwnerUserID == nil || *allowed[0].OwnerUserID != memberID {
		t.Fatalf("non-proxy node line management = %+v, %v", allowed, err)
	}
	if _, err := repo.GetUsableLine(ctx, memberID, allowed[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-proxy node line usable for traffic: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET capabilities=ARRAY['proxy']::text[],proxy_port=443 WHERE id=$1`, proxyID); err != nil {
		t.Fatal(err)
	}
	var currentOwnID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM lines WHERE owner_user_id=$1`, memberID).Scan(&currentOwnID); err != nil {
		t.Fatal(err)
	}
	updatedOwn, err := repo.UpdateOwnLine(ctx, memberID, currentOwnID, LinePatch{Name: stringPtr(" Renamed Own "), Enabled: boolPtr(false), Weight: intPtr(3)}, "update-own")
	if err != nil || updatedOwn.Name != "Renamed Own" || updatedOwn.Enabled || updatedOwn.Weight != 3 {
		t.Fatalf("updated own line = %+v, %v", updatedOwn, err)
	}
	if _, err := repo.UpdateOwnLine(ctx, memberID, shared.ID, LinePatch{Enabled: boolPtr(false)}, "wrong-owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("member updated shared line = %v", err)
	}
	updatedShared, err := repo.UpdateSharedLine(ctx, shared.ID, LinePatch{Enabled: boolPtr(false), Priority: intPtr(7), MultiplierMilli: intPtr(2000), Tags: &[]string{"premium"}}, actorID, "update-shared")
	if err != nil || updatedShared.Enabled || updatedShared.Priority != 7 || updatedShared.MultiplierMilli == nil || *updatedShared.MultiplierMilli != 2000 || len(updatedShared.Tags) != 1 || updatedShared.Tags[0] != "premium" {
		t.Fatalf("updated shared line = %+v, %v", updatedShared, err)
	}
	if _, err := repo.CreateSharedLine(ctx, LineInput{Name: "Shared US", NodeID: otherProxyID, Enabled: true, Priority: 100, Weight: 1, Tags: []string{}}, actorID, "shared-us"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateSharedLine(ctx, shared.ID, LinePatch{Name: stringPtr("Shared US")}, actorID, "duplicate-shared-name"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate shared name update = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET ends_at=clock_timestamp()-interval '1 second' WHERE user_id=$1`, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateOwnLine(ctx, memberID, currentOwnID, LinePatch{Name: stringPtr("Expired Edit")}, "expired-edit"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired membership updated own line = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET ends_at=clock_timestamp()+interval '1 day' WHERE user_id=$1`, memberID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteOwnLine(ctx, memberID, shared.ID, "delete-shared"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("member deleted shared line = %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plan_line_grants(plan_id,line_id) VALUES ($1,$2)`, planID, currentOwnID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteOwnLine(ctx, memberID, currentOwnID, "delete-referenced"); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted referenced line = %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM plan_line_grants WHERE line_id=$1`, currentOwnID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteOwnLine(ctx, memberID, currentOwnID, "delete-own"); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteOwnLine(ctx, memberID, currentOwnID, "delete-own-again"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted line again = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memberships SET ends_at=clock_timestamp()+interval '1 second' WHERE user_id=$1`, memberID); err != nil {
		t.Fatal(err)
	}
	lockTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback(ctx)
	if _, err := lockTx.Exec(ctx, `UPDATE nodes SET enabled=enabled WHERE id=$1`, proxyID); err != nil {
		t.Fatal(err)
	}
	boundary := make(chan error, 1)
	go func() {
		_, err := repo.CreateCustomLine(ctx, LineInput{Name: "Expiry Boundary", NodeID: proxyID, Enabled: true, Priority: 100, Weight: 1, Tags: []string{}}, memberID, "expiry-boundary")
		boundary <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-boundary:
			t.Fatalf("line did not wait on node lock: %v", err)
		default:
		}
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
WHERE datname=current_database() AND wait_event_type='Lock'
AND query LIKE 'SELECT n.group_id::text,n.enabled,g.enabled,n.capabilities,n.proxy_port%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("line creation did not wait on node lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		var expired bool
		if err := pool.QueryRow(ctx, `SELECT clock_timestamp()>=ends_at FROM memberships WHERE user_id=$1`, memberID).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-boundary:
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("line committed after membership expiry = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("line creation did not finish after node lock")
	}
}
