package agentproto

import (
	"encoding/json"
	"testing"
)

func TestDecodeHelloValidatesVersionRevisionAndCapabilities(t *testing.T) {
	valid := []byte(`{"agent_version":"1.0.0","applied_revision":3,"capabilities":["forward","relay"]}`)
	value, err := DecodeHello(valid)
	if err != nil || value.AppliedRevision != 3 || value.AgentVersion != "1.0.0" {
		t.Fatalf("valid hello = %+v, %v", value, err)
	}
	for _, payload := range [][]byte{
		[]byte(`{"agent_version":"1.0.0","capabilities":["forward"]}`),
		[]byte(`{"agent_version":"","applied_revision":0,"capabilities":["forward"]}`),
		[]byte(`{"agent_version":"1.0.0","applied_revision":-1,"capabilities":["forward"]}`),
		[]byte(`{"agent_version":"1.0.0","applied_revision":0,"capabilities":["unknown"]}`),
		[]byte(`{"agent_version":"1.0.0","applied_revision":0,"capabilities":["forward","forward"]}`),
		[]byte(`{"agent_version":"1.0.0","applied_revision":0,"capabilities":[],"extra":true}`),
		[]byte(`{"agent_version":"1.0.0","agent_version":"2.0.0","applied_revision":0,"capabilities":["forward"]}`),
	} {
		if _, err := DecodeHello(payload); err == nil {
			t.Fatalf("accepted malformed hello: %s", payload)
		}
	}
}

func TestDecodeHeartbeatValidatesMetricsAndEngineState(t *testing.T) {
	valid := Heartbeat{UptimeSeconds: 100, CPUPct: 12.5, MemoryUsedBytes: 1024, RXBytes: 500,
		TXBytes: 300, Connections: 2, EngineStatus: "running"}
	data, _ := json.Marshal(valid)
	value, err := DecodeHeartbeat(data)
	if err != nil || value.Connections != 2 || value.CPUPct != 12.5 {
		t.Fatalf("valid heartbeat = %+v, %v", value, err)
	}
	for _, payload := range [][]byte{
		[]byte(`{"uptime_seconds":1,"cpu_pct":0,"memory_used_bytes":0,"rx_bytes":0,"tx_bytes":0,"engine_status":"running"}`),
		[]byte(`{"uptime_seconds":1,"cpu_pct":101,"memory_used_bytes":0,"rx_bytes":0,"tx_bytes":0,"connections":0,"engine_status":"running"}`),
		[]byte(`{"uptime_seconds":1,"cpu_pct":0,"memory_used_bytes":-1,"rx_bytes":0,"tx_bytes":0,"connections":0,"engine_status":"running"}`),
		[]byte(`{"uptime_seconds":1,"cpu_pct":0,"memory_used_bytes":0,"rx_bytes":0,"tx_bytes":0,"connections":-1,"engine_status":"running"}`),
		[]byte(`{"uptime_seconds":1,"cpu_pct":0,"memory_used_bytes":0,"rx_bytes":0,"tx_bytes":0,"connections":0,"engine_status":"unknown"}`),
		[]byte(`{"uptime_seconds":1,"cpu_pct":0,"memory_used_bytes":0,"rx_bytes":0,"tx_bytes":0,"connections":0,"engine_status":"running","rx_bytes":1}`),
	} {
		if _, err := DecodeHeartbeat(payload); err == nil {
			t.Fatalf("accepted malformed heartbeat: %s", payload)
		}
	}
}
