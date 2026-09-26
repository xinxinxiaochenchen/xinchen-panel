package agentruntime

type Stats struct {
	Connections int64
}

func (r *Runtime) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result Stats
	for key, listener := range r.listeners {
		switch key.protocol {
		case "TCP":
			listener.tcpMu.Lock()
			result.Connections += int64(len(listener.tcpSessions))
			listener.tcpMu.Unlock()
		case "UDP":
			listener.udpMu.Lock()
			result.Connections += int64(len(listener.sessions))
			listener.udpMu.Unlock()
		}
	}
	return result
}
