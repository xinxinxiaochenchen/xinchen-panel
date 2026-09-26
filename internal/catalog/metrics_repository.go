package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (r *PostgresRepository) GetNodeMetrics(ctx context.Context, nodeID string) (NodeMetrics, error) {
	const query = `SELECT n.id::text,
CASE WHEN a.status='revoked' THEN 'revoked'
     WHEN a.status='online' AND a.last_seen_at >= now()-interval '45 seconds' THEN 'online'
     WHEN a.id IS NULL THEN 'unknown' ELSE 'offline' END,
a.last_seen_at,
COALESCE(a.status='online' AND a.last_seen_at >= now()-interval '45 seconds'
         AND m.observed_at >= now()-interval '45 seconds', false),
m.observed_at,m.uptime_seconds,m.cpu_pct,m.memory_used_bytes,m.rx_bytes,m.tx_bytes,m.connections,m.engine_status
FROM nodes n LEFT JOIN agents a ON a.node_id=n.id LEFT JOIN agent_metrics m ON m.node_id=n.id
WHERE n.id=$1`
	var result NodeMetrics
	var lastSeen, observedAt pgtype.Timestamptz
	var uptime, memory, rx, tx, connections pgtype.Int8
	var cpu pgtype.Float8
	var engine pgtype.Text
	err := r.pool.QueryRow(ctx, query, nodeID).Scan(&result.NodeID, &result.AgentStatus, &lastSeen, &result.Fresh,
		&observedAt, &uptime, &cpu, &memory, &rx, &tx, &connections, &engine)
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeMetrics{}, ErrNotFound
	}
	if err != nil {
		return NodeMetrics{}, fmt.Errorf("get node metrics: %w", err)
	}
	if lastSeen.Valid {
		value := lastSeen.Time
		result.LastSeenAt = &value
	}
	if observedAt.Valid {
		result.Metrics = &AgentMetrics{
			ObservedAt: observedAt.Time, UptimeSeconds: uptime.Int64, CPUPct: cpu.Float64,
			MemoryUsedBytes: memory.Int64, RXBytes: rx.Int64, TXBytes: tx.Int64,
			Connections: connections.Int64, EngineStatus: engine.String,
		}
	}
	return result, nil
}
