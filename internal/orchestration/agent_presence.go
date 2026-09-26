package orchestration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/agentproto"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAgentPresenceDenied = errors.New("Agent is revoked, disabled or missing")

type AgentPresenceRepository struct{ pool *pgxpool.Pool }

func NewAgentPresenceRepository(pool *pgxpool.Pool) *AgentPresenceRepository {
	return &AgentPresenceRepository{pool: pool}
}

func (r *AgentPresenceRepository) MarkOnline(ctx context.Context, nodeID, version string, capabilities []string) error {
	result, err := r.pool.Exec(ctx, `UPDATE agents a SET status='online',version=$2,capabilities=$3,last_seen_at=clock_timestamp()
FROM nodes n WHERE a.node_id=$1 AND n.id=a.node_id AND n.enabled AND a.status <> 'revoked'`, nodeID, version, capabilities)
	if err != nil {
		return fmt.Errorf("mark Agent online: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrAgentPresenceDenied
	}
	return nil
}

func (r *AgentPresenceRepository) RecordHeartbeat(ctx context.Context, nodeID string, value agentproto.Heartbeat) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin Agent heartbeat: %w", err)
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `UPDATE agents a SET status='online',last_seen_at=clock_timestamp()
FROM nodes n WHERE a.node_id=$1 AND n.id=a.node_id AND n.enabled AND a.status <> 'revoked'`, nodeID)
	if err != nil {
		return fmt.Errorf("update Agent heartbeat: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrAgentPresenceDenied
	}
	_, err = tx.Exec(ctx, `INSERT INTO agent_metrics(node_id,observed_at,uptime_seconds,cpu_pct,memory_used_bytes,rx_bytes,tx_bytes,connections,engine_status)
VALUES ($1,clock_timestamp(),$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (node_id) DO UPDATE SET observed_at=EXCLUDED.observed_at,uptime_seconds=EXCLUDED.uptime_seconds,
cpu_pct=EXCLUDED.cpu_pct,memory_used_bytes=EXCLUDED.memory_used_bytes,rx_bytes=EXCLUDED.rx_bytes,
tx_bytes=EXCLUDED.tx_bytes,connections=EXCLUDED.connections,engine_status=EXCLUDED.engine_status`, nodeID,
		value.UptimeSeconds, value.CPUPct, value.MemoryUsedBytes, value.RXBytes, value.TXBytes, value.Connections, value.EngineStatus)
	if err != nil {
		return fmt.Errorf("store Agent heartbeat metrics: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Agent heartbeat: %w", err)
	}
	return nil
}

func (r *AgentPresenceRepository) MarkOffline(ctx context.Context, nodeID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE agents SET status='offline' WHERE node_id=$1 AND status='online'`, nodeID)
	if err != nil {
		return fmt.Errorf("mark Agent offline: %w", err)
	}
	return nil
}

func (r *AgentPresenceRepository) MarkOfflineStale(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.pool.Exec(ctx, `UPDATE agents SET status='offline' WHERE status='online' AND last_seen_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("sweep stale Agents: %w", err)
	}
	return result.RowsAffected(), nil
}
