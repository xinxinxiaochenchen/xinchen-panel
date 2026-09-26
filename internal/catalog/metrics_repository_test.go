package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresNodeMetricsFreshAndStale(t *testing.T) {
	databaseURL := catalogTestDatabaseURL()
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
	if err := pool.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region) VALUES (gen_random_uuid(),'TEST.METRICS','Metrics test','JP') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM resource_groups WHERE id=$1`, groupID) })
	if err := pool.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,capabilities) VALUES (gen_random_uuid(),$1,'Metrics node','JP','metrics.example.invalid',ARRAY['forward']) RETURNING id::text`, groupID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM nodes WHERE id=$1`, nodeID) })
	repo := NewPostgresRepository(pool)
	unknown, err := repo.GetNodeMetrics(ctx, nodeID)
	if err != nil || unknown.AgentStatus != "unknown" || unknown.Metrics != nil || unknown.Fresh {
		t.Fatalf("unknown metrics = %+v, %v", unknown, err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO agents(id,node_id,status,last_seen_at) VALUES (gen_random_uuid(),$1,'online',now())`, nodeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM agents WHERE node_id=$1`, nodeID) })
	if _, err := pool.Exec(ctx, `INSERT INTO agent_metrics(node_id,uptime_seconds,cpu_pct,memory_used_bytes,rx_bytes,tx_bytes,connections,engine_status)
VALUES ($1,3600,12.5,1048576,2000,3000,7,'running')`, nodeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM agent_metrics WHERE node_id=$1`, nodeID) })
	fresh, err := repo.GetNodeMetrics(ctx, nodeID)
	if err != nil || fresh.AgentStatus != "online" || !fresh.Fresh || fresh.Metrics == nil || fresh.Metrics.Connections != 7 || fresh.Metrics.CPUPct != 12.5 {
		t.Fatalf("fresh metrics = %+v, %v", fresh, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agents SET last_seen_at=clock_timestamp()+interval '1 second' WHERE node_id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	reconnected, err := repo.GetNodeMetrics(ctx, nodeID)
	if err != nil || reconnected.AgentStatus != "online" || reconnected.Fresh || reconnected.Metrics == nil {
		t.Fatalf("pre-heartbeat reconnect metrics = %+v, %v", reconnected, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agents SET last_seen_at=now()-interval '2 minutes' WHERE node_id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent_metrics SET observed_at=now()-interval '2 minutes' WHERE node_id=$1`, nodeID); err != nil {
		t.Fatal(err)
	}
	stale, err := repo.GetNodeMetrics(ctx, nodeID)
	if err != nil || stale.AgentStatus != "offline" || stale.Fresh || stale.Metrics == nil || stale.Metrics.Connections != 7 {
		t.Fatalf("stale metrics = %+v, %v", stale, err)
	}
	if _, err := repo.GetNodeMetrics(ctx, "44444444-4444-7444-8444-444444444444"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing node error = %v", err)
	}
}
