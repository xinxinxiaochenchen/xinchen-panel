package agentruntime

import "testing"

func TestRuntimeStatsCountsActiveTCPAndUDPConnections(t *testing.T) {
	r := New(Options{})
	tcp := &endpoint{tcpSessions: make(map[*tcpSession]struct{})}
	tcp.tcpSessions[&tcpSession{}] = struct{}{}
	tcp.tcpSessions[&tcpSession{}] = struct{}{}
	udp := &endpoint{sessions: map[string]*udpSession{"a": {}, "b": {}, "c": {}}}
	r.listeners[listenerKey{protocol: "TCP", port: 24000}] = tcp
	r.listeners[listenerKey{protocol: "UDP", port: 24001}] = udp
	if stats := r.Stats(); stats.Connections != 5 {
		t.Fatalf("active connections = %d", stats.Connections)
	}
}
