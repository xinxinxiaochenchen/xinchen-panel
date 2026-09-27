package orchestration

import (
	"context"
	"fmt"
	"slices"

	"controlplane/internal/agentruntime"
	"github.com/jackc/pgx/v5"
)

// readRelayLineFacts reads every ordered multi-hop topology in the same
// transaction as the node's ordinary forward/proxy facts. A route is only
// executable when every hop has a live Agent and both its control certificate
// and relay server certificate are still authorized.
func readRelayLineFacts(ctx context.Context, tx pgx.Tx, nodeID string) ([]RelayLineFacts, error) {
	rows, err := tx.Query(ctx, `
SELECT l.id::text,l.relay_generation,l.enabled,
       h.position,h.node_id::text,h.role,n.host,COALESCE(n.relay_port,0),
       n.enabled,g.enabled,
       COALESCE('forward'=ANY(n.capabilities) AND 'relay'=ANY(a.capabilities),false),
       COALESCE('proxy'=ANY(n.capabilities) AND 'proxy'=ANY(a.capabilities),false),
       COALESCE(a.status='online' AND a.last_seen_at > clock_timestamp()-interval '45 seconds',false),
       array_remove(ARRAY[CASE WHEN a.cert_expires_at>clock_timestamp() THEN a.cert_fingerprint END] || COALESCE((SELECT array_agg(DISTINCT cg.fingerprint ORDER BY cg.fingerprint) FROM agent_certificate_grants cg WHERE cg.node_id=n.id AND cg.expires_at>clock_timestamp()),ARRAY[]::text[]),NULL),
       COALESCE((SELECT array_agg(DISTINCT rg.fingerprint ORDER BY rg.fingerprint) FROM agent_relay_certificate_grants rg WHERE rg.node_id=n.id AND rg.expires_at>clock_timestamp()),ARRAY[]::text[])
FROM lines l
JOIN (SELECT line_id FROM line_hops GROUP BY line_id HAVING count(*) BETWEEN 2 AND 8) multi ON multi.line_id=l.id
JOIN line_hops h ON h.line_id=l.id
JOIN nodes n ON n.id=h.node_id
JOIN resource_groups g ON g.id=n.group_id
LEFT JOIN agents a ON a.node_id=n.id
WHERE l.enabled AND EXISTS (SELECT 1 FROM line_hops mine WHERE mine.line_id=l.id AND mine.node_id=$1)
ORDER BY l.id,h.position`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query relay topology facts: %w", err)
	}
	defer rows.Close()
	byLine := make(map[string]*RelayLineFacts)
	ordered := make([]string, 0)
	for rows.Next() {
		var lineID, nodeID, role, host string
		var generation int64
		var enabled, nodeEnabled, groupEnabled, relayCapable, proxyCapable, online bool
		var position, relayPort int
		var agentFingerprints, relayFingerprints []string
		if err := rows.Scan(&lineID, &generation, &enabled, &position, &nodeID, &role, &host, &relayPort,
			&nodeEnabled, &groupEnabled, &relayCapable, &proxyCapable, &online, &agentFingerprints, &relayFingerprints); err != nil {
			return nil, fmt.Errorf("scan relay topology facts: %w", err)
		}
		if generation < 1 {
			return nil, fmt.Errorf("invalid relay generation for line %s", lineID)
		}
		line := byLine[lineID]
		if line == nil {
			line = &RelayLineFacts{LineID: lineID, Generation: uint64(generation), Enabled: enabled, Hops: make([]RelayHopFact, 0, 8)}
			byLine[lineID] = line
			ordered = append(ordered, lineID)
		}
		if position != len(line.Hops) {
			return nil, fmt.Errorf("relay topology positions are not contiguous for line %s", lineID)
		}
		line.Hops = append(line.Hops, RelayHopFact{NodeID: nodeID, Role: agentruntime.RelayRole(role), Host: host,
			RelayPort: relayPort, Online: online && nodeEnabled && groupEnabled, RelayCapable: relayCapable,
			ProxyCapable: proxyCapable, AgentFingerprints: agentFingerprints, RelayFingerprints: relayFingerprints})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate relay topology facts: %w", err)
	}
	slices.Sort(ordered)
	result := make([]RelayLineFacts, 0, len(ordered))
	for _, lineID := range ordered {
		result = append(result, *byLine[lineID])
	}
	return result, nil
}
