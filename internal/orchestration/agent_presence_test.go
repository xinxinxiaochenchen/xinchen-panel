package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAgentPresenceAndHeartbeatMetrics(t *testing.T) {
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
	var groupID, nodeID string
	if err := pool.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region)
VALUES (gen_random_uuid(),gen_random_uuid()::text,'Presence','US') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM resource_groups WHERE id=$1`, groupID)
	if err := pool.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,capabilities)
VALUES (gen_random_uuid(),$1,'Presence node','US',gen_random_uuid()::text || '.example.org',ARRAY['forward']) RETURNING id::text`, groupID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, nodeID)
	if _, err := pool.Exec(ctx, `INSERT INTO agents(id,node_id) VALUES (gen_random_uuid(),$1)`, nodeID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM agents WHERE node_id=$1`, nodeID)
	repo := NewAgentPresenceRepository(pool)
	if err := repo.MarkOnline(ctx, nodeID, "1.0.0", []string{"forward"}); err != nil {
		t.Fatal(err)
	}
	heartbeat := agentproto.Heartbeat{UptimeSeconds: 30, CPUPct: 12.5, MemoryUsedBytes: 1024,
		RXBytes: 100, TXBytes: 200, Connections: 3, EngineStatus: "running"}
	if err := repo.RecordHeartbeat(ctx, nodeID, heartbeat); err != nil {
		t.Fatal(err)
	}
	var status, version, engine string
	var lastSeen time.Time
	var cpu float64
	var connections int64
	if err := pool.QueryRow(ctx, `SELECT a.status,a.version,a.last_seen_at,m.cpu_pct,m.connections,m.engine_status
FROM agents a JOIN agent_metrics m ON m.node_id=a.node_id WHERE a.node_id=$1`, nodeID).
		Scan(&status, &version, &lastSeen, &cpu, &connections, &engine); err != nil {
		t.Fatal(err)
	}
	if status != "online" || version != "1.0.0" || cpu != 12.5 || connections != 3 || engine != "running" || time.Since(lastSeen) > time.Minute {
		t.Fatalf("presence = %s %s %s %f %d %s", status, version, lastSeen, cpu, connections, engine)
	}
	if _, err := pool.Exec(ctx, `UPDATE agents SET last_seen_at=now()-interval '1 minute' WHERE node_id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	if count, err := repo.MarkOfflineStale(ctx, time.Now().Add(-45*time.Second)); err != nil || count < 1 {
		t.Fatalf("offline sweep = %d, %v", count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM agents WHERE node_id=$1`, nodeID).Scan(&status); err != nil || status != "offline" {
		t.Fatalf("status = %s, %v", status, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agents SET status='revoked' WHERE node_id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkOnline(ctx, nodeID, "1.0.0", []string{"forward"}); !errors.Is(err, ErrAgentPresenceDenied) {
		t.Fatalf("revoked online = %v", err)
	}
	if err := repo.RecordHeartbeat(ctx, nodeID, heartbeat); !errors.Is(err, ErrAgentPresenceDenied) {
		t.Fatalf("revoked heartbeat = %v", err)
	}
}
