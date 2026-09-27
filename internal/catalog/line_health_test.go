package catalog

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresLineHealthHonorsEntitlementAndAgentState(t *testing.T) {
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
	suffix := fmt.Sprint(time.Now().UnixNano())
	var actor, member, group, node, plan, line string
	insert := func(out *string, query string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, query, args...).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	insert(&actor, `INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "health-admin-"+suffix+"@example.invalid")
	insert(&member, `INSERT INTO users(id,email,password_hash,status) VALUES(gen_random_uuid(),$1,'hash','active') RETURNING id::text`, "health-user-"+suffix+"@example.invalid")
	insert(&group, `INSERT INTO resource_groups(id,code,name,region) VALUES(gen_random_uuid(),$1,$1,'JP') RETURNING id::text`, "HEALTH."+suffix)
	insert(&node, `INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities) VALUES(gen_random_uuid(),$1,'Health Node','JP',$2,443,ARRAY['proxy']) RETURNING id::text`, group, "health-"+suffix+".example.invalid")
	insert(&plan, `INSERT INTO plans(id,name,quota_bytes) VALUES(gen_random_uuid(),$1,1000000) RETURNING id::text`, "health-plan-"+suffix)
	insert(&line, `INSERT INTO lines(id,owner_user_id,name,created_by) VALUES(gen_random_uuid(),$1,$2,$3) RETURNING id::text`, member, "Health line "+suffix, actor)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM config_revisions WHERE node_id=$1`, node)
		_, _ = pool.Exec(cleanup, `DELETE FROM agents WHERE node_id=$1`, node)
		_, _ = pool.Exec(cleanup, `DELETE FROM lines WHERE id=$1`, line)
		_, _ = pool.Exec(cleanup, `DELETE FROM memberships WHERE user_id=$1`, member)
		_, _ = pool.Exec(cleanup, `DELETE FROM plans WHERE id=$1`, plan)
		_, _ = pool.Exec(cleanup, `DELETE FROM nodes WHERE id=$1`, node)
		_, _ = pool.Exec(cleanup, `DELETE FROM resource_groups WHERE id=$1`, group)
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE id=ANY($1::uuid[])`, []string{actor, member})
	})
	exec(`INSERT INTO line_hops(line_id,position,node_id,role) VALUES($1,0,$2,'egress')`, line, node)
	exec(`INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES(gen_random_uuid(),$1,$2,now()-interval '1 hour',now()+interval '1 day','active',1,'UTC',jsonb_build_object('resource_group_ids',jsonb_build_array($3::text),'limits',jsonb_build_object('allow_custom_lines',true)))`, member, plan, group)
	repo := NewPostgresRepository(pool)
	health, err := repo.GetAllowedLineHealth(ctx, member, line)
	if err != nil || health.State != "unavailable" || health.Reason != "agent_offline" || len(health.Hops) != 1 {
		t.Fatalf("offline health = %+v, %v", health, err)
	}
	if _, err := repo.GetAllowedLineHealth(ctx, actor, line); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unauthorized line health = %v", err)
	}
	exec(`INSERT INTO agents(id,node_id,cert_fingerprint,cert_expires_at,capabilities,status,last_seen_at) VALUES(gen_random_uuid(),$1,$2,now()+interval '1 day',ARRAY['proxy'],'online',now())`, node, fmt.Sprintf("%064x", 1))
	health, err = repo.GetAllowedLineHealth(ctx, member, line)
	if err != nil || health.State != "ready" || health.Reason != "" {
		t.Fatalf("ready health = %+v, %v", health, err)
	}
	exec(`UPDATE memberships SET status='expired' WHERE user_id=$1`, member)
	if _, err := repo.GetAllowedLineHealth(ctx, member, line); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired membership health = %v", err)
	}
}

func TestEvaluateLineHealth(t *testing.T) {
	ready := []LineHopHealth{
		{Position: 0, Role: "ingress", NodeEnabled: true, GroupEnabled: true, ProxyCapable: true, RelayCapable: true, ProxyPortReady: true, RelayPortReady: true, AgentOnline: true, CertificateReady: true, RelayCertificateReady: true, RelayApplied: true, DesiredRevision: 2, AppliedRevision: 2},
		{Position: 1, Role: "egress", NodeEnabled: true, GroupEnabled: true, RelayCapable: true, RelayPortReady: true, AgentOnline: true, CertificateReady: true, RelayCertificateReady: true, RelayApplied: true, DesiredRevision: 3, AppliedRevision: 3},
	}
	for _, tc := range []struct {
		name                  string
		enabled               bool
		hops                  []LineHopHealth
		wantState, wantReason string
	}{
		{"ready", true, ready, "ready", ""},
		{"disabled", false, ready, "disabled", "line_disabled"},
		{"offline", true, mutateHealth(ready, 1, func(h *LineHopHealth) { h.AgentOnline = false }), "unavailable", "agent_offline"},
		{"certificate", true, mutateHealth(ready, 1, func(h *LineHopHealth) { h.RelayCertificateReady = false }), "unavailable", "certificate_unavailable"},
		{"pending", true, mutateHealth(ready, 1, func(h *LineHopHealth) { h.RelayApplied = false }), "converging", "relay_pending"},
		{"revision", true, mutateHealth(ready, 1, func(h *LineHopHealth) { h.DesiredRevision = 4 }), "converging", "relay_pending"},
		{"node", true, mutateHealth(ready, 1, func(h *LineHopHealth) { h.NodeEnabled = false }), "unavailable", "node_disabled"},
		{"single hop ready", true, []LineHopHealth{{Position: 0, Role: "egress", NodeEnabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPortReady: true, AgentOnline: true, CertificateReady: true, DesiredRevision: 2, AppliedRevision: 2}}, "ready", ""},
		{"single hop pending", true, []LineHopHealth{{Position: 0, Role: "egress", NodeEnabled: true, GroupEnabled: true, ProxyCapable: true, ProxyPortReady: true, AgentOnline: true, CertificateReady: true, DesiredRevision: 3, AppliedRevision: 2}}, "converging", "config_pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, reason := evaluateLineHealth(tc.enabled, tc.hops)
			if state != tc.wantState || reason != tc.wantReason {
				t.Fatalf("got %s/%s, want %s/%s", state, reason, tc.wantState, tc.wantReason)
			}
		})
	}
}

func mutateHealth(source []LineHopHealth, index int, change func(*LineHopHealth)) []LineHopHealth {
	copyOf := append([]LineHopHealth(nil), source...)
	change(&copyOf[index])
	return copyOf
}
