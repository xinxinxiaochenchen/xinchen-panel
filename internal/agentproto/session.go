package agentproto

import (
	"encoding/json"
	"errors"
	"regexp"
)

var agentVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

type Hello struct {
	AgentVersion    string   `json:"agent_version"`
	AppliedRevision int64    `json:"applied_revision"`
	Capabilities    []string `json:"capabilities"`
}

func DecodeHello(payload []byte) (Hello, error) {
	var value Hello
	if err := decodeStrictPayload(payload, &value); err != nil {
		return Hello{}, err
	}
	if !hasFields(payload, "agent_version", "applied_revision", "capabilities") {
		return Hello{}, errors.New("missing Agent hello field")
	}
	if !agentVersionPattern.MatchString(value.AgentVersion) || value.AppliedRevision < 0 || len(value.Capabilities) == 0 || len(value.Capabilities) > 4 {
		return Hello{}, errors.New("invalid Agent hello")
	}
	seen := map[string]bool{}
	for _, capability := range value.Capabilities {
		if capability != "forward" && capability != "proxy" && capability != "relay" && capability != "proxy_candidates" || seen[capability] {
			return Hello{}, errors.New("invalid Agent capability")
		}
		seen[capability] = true
	}
	return value, nil
}

type Heartbeat struct {
	UptimeSeconds   int64   `json:"uptime_seconds"`
	CPUPct          float64 `json:"cpu_pct"`
	MemoryUsedBytes int64   `json:"memory_used_bytes"`
	RXBytes         int64   `json:"rx_bytes"`
	TXBytes         int64   `json:"tx_bytes"`
	Connections     int64   `json:"connections"`
	EngineStatus    string  `json:"engine_status"`
}

func DecodeHeartbeat(payload []byte) (Heartbeat, error) {
	var value Heartbeat
	if err := decodeStrictPayload(payload, &value); err != nil {
		return Heartbeat{}, err
	}
	if !hasFields(payload, "uptime_seconds", "cpu_pct", "memory_used_bytes", "rx_bytes", "tx_bytes", "connections", "engine_status") {
		return Heartbeat{}, errors.New("missing Agent heartbeat field")
	}
	if value.UptimeSeconds < 0 || value.CPUPct < 0 || value.CPUPct > 100 || value.MemoryUsedBytes < 0 ||
		value.RXBytes < 0 || value.TXBytes < 0 || value.Connections < 0 ||
		(value.EngineStatus != "running" && value.EngineStatus != "degraded" && value.EngineStatus != "stopped") {
		return Heartbeat{}, errors.New("invalid Agent heartbeat")
	}
	return value, nil
}

func hasFields(payload []byte, required ...string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(payload, &object) != nil {
		return false
	}
	for _, key := range required {
		value, ok := object[key]
		if !ok || string(value) == "null" {
			return false
		}
	}
	return true
}
