package catalog

import "time"

// AgentMetrics is the most recent Agent heartbeat. RX/TX are node interface
// counters, separate from user traffic accounting.
type AgentMetrics struct {
	ObservedAt      time.Time `json:"observed_at"`
	UptimeSeconds   int64     `json:"uptime_seconds"`
	CPUPct          float64   `json:"cpu_pct"`
	MemoryUsedBytes int64     `json:"memory_used_bytes"`
	RXBytes         int64     `json:"rx_bytes"`
	TXBytes         int64     `json:"tx_bytes"`
	Connections     int64     `json:"connections"`
	EngineStatus    string    `json:"engine_status"`
}

type NodeMetrics struct {
	NodeID      string        `json:"node_id"`
	AgentStatus string        `json:"agent_status"`
	LastSeenAt  *time.Time    `json:"last_seen_at"`
	LatencyMS   *int          `json:"latency_ms"`
	Fresh       bool          `json:"fresh"`
	Metrics     *AgentMetrics `json:"metrics"`
}
