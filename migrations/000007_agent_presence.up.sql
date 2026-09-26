CREATE TABLE agent_metrics (
    node_id uuid PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    observed_at timestamptz NOT NULL DEFAULT now(),
    uptime_seconds bigint NOT NULL CHECK (uptime_seconds >= 0),
    cpu_pct double precision NOT NULL CHECK (cpu_pct >= 0 AND cpu_pct <= 100),
    memory_used_bytes bigint NOT NULL CHECK (memory_used_bytes >= 0),
    rx_bytes bigint NOT NULL CHECK (rx_bytes >= 0),
    tx_bytes bigint NOT NULL CHECK (tx_bytes >= 0),
    connections bigint NOT NULL CHECK (connections >= 0),
    engine_status text NOT NULL CHECK (engine_status IN ('running', 'degraded', 'stopped'))
);
CREATE INDEX agents_status_last_seen_idx ON agents(status, last_seen_at);
