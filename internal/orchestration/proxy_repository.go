package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"controlplane/internal/agentruntime"
	"github.com/jackc/pgx/v5"
)

// Proxy facts are read within the same repeatable-read transaction as forward
// rules, so a complete node revision has one authorization view.
func readProxyFacts(ctx context.Context, tx pgx.Tx, nodeID string, relayLines []RelayLineFacts, relayConfigs map[string][]agentruntime.RelayConfig) ([]ProxyFacts, error) {
	rows, err := tx.Query(ctx, `SELECT a.id::text,a.user_id::text,a.line_id::text,h.node_id::text,n.group_id::text,
a.credential_hash,a.enabled,u.status='active',l.enabled,COALESCE(l.owner_user_id::text,''),l.relay_generation,
ARRAY(SELECT hn.group_id::text FROM line_hops hh JOIN nodes hn ON hn.id=hh.node_id WHERE hh.line_id=l.id ORDER BY hh.position),
m.snapshot_json,m.ends_at
FROM proxy_accesses a JOIN users u ON u.id=a.user_id JOIN lines l ON l.id=a.line_id
JOIN line_hops h ON h.line_id=l.id AND h.position=0 AND h.role IN ('egress','ingress')
JOIN nodes n ON n.id=h.node_id
LEFT JOIN LATERAL (SELECT snapshot_json,ends_at FROM memberships mm
WHERE mm.user_id=a.user_id AND mm.status='active' AND mm.starts_at<=statement_timestamp()
AND mm.ends_at>statement_timestamp() LIMIT 1) m ON true
WHERE h.node_id=$1 AND a.enabled
ORDER BY a.id`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query proxy snapshot facts: %w", err)
	}
	defer rows.Close()
	facts := make([]ProxyFacts, 0)
	for rows.Next() {
		var fact ProxyFacts
		var snapshot []byte
		var expires *time.Time
		var relayGeneration int64
		if err := rows.Scan(&fact.ID, &fact.OwnerID, &fact.LineID, &fact.NodeID, &fact.GroupID, &fact.CredentialHash,
			&fact.Enabled, &fact.OwnerActive, &fact.LineEnabled, &fact.LineOwnerID, &relayGeneration, &fact.HopGroupIDs, &snapshot, &expires); err != nil {
			return nil, fmt.Errorf("scan proxy facts: %w", err)
		}
		if relayGeneration < 0 {
			return nil, fmt.Errorf("invalid relay generation for proxy line %s", fact.LineID)
		}
		fact.HopCount = len(fact.HopGroupIDs)
		if fact.HopCount > 1 {
			fact.RelayGeneration = uint64(relayGeneration)
		}
		if fact.RelayGeneration != 0 {
			for _, line := range relayLines {
				if line.LineID != fact.LineID || len(line.Hops) == 0 || line.Generation != fact.RelayGeneration {
					continue
				}
				if downstreamRelayApplied(line) {
					for _, relay := range relayConfigs[fact.NodeID] {
						if relay.LineID == fact.LineID && relay.Generation == fact.RelayGeneration && relay.Role == agentruntime.RelayIngress {
							fact.RelayReady = true
							break
						}
					}
				}
				break
			}
		}
		if snapshot != nil && expires != nil {
			var grant struct {
				ResourceGroupIDs []string `json:"resource_group_ids"`
				LineIDs          []string `json:"line_ids"`
				Limits           struct {
					AllowCustomLines bool `json:"allow_custom_lines"`
					MaxHops          int  `json:"max_hops"`
				} `json:"limits"`
			}
			if err := json.Unmarshal(snapshot, &grant); err != nil {
				fact.EligibilityError = "invalid proxy membership snapshot"
			} else {
				fact.MembershipActive = true
				fact.MemberGroupIDs = grant.ResourceGroupIDs
				fact.MemberLineIDs = grant.LineIDs
				fact.AllowCustomLines = grant.Limits.AllowCustomLines
				fact.MaxHops = grant.Limits.MaxHops
				fact.ExpiresAt = *expires
			}
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read proxy facts: %w", err)
	}
	return facts, nil
}

func recordProxyApplyStatus(ctx context.Context, tx pgx.Tx, nodeID string, revision int64, payload []byte, status string, acceptNewer bool) error {
	_, err := tx.Exec(ctx, `WITH executable AS (
SELECT p->>'id' AS id,p->>'credential_hash' AS hash FROM jsonb_array_elements(COALESCE($2::jsonb->'proxy_config','[]'::jsonb)) p)
UPDATE proxy_accesses a SET apply_status=CASE
WHEN $3='rejected' THEN 'apply_failed'
WHEN EXISTS(SELECT 1 FROM executable e WHERE e.id=a.id::text AND e.hash=a.credential_hash) THEN 'active'
ELSE 'disabled' END
FROM line_hops h,config_revisions cr
WHERE h.line_id=a.line_id AND h.position=0 AND h.node_id=$1
AND cr.node_id=$1 AND cr.revision=$4 AND ($5 OR a.updated_at<=cr.created_at)
AND NOT EXISTS(SELECT 1 FROM executable e WHERE e.id=a.id::text AND e.hash<>a.credential_hash)
AND ($3='applied' OR EXISTS(SELECT 1 FROM executable e WHERE e.id=a.id::text AND e.hash=a.credential_hash))`, nodeID, payload, status, revision, acceptNewer)
	if err != nil {
		return fmt.Errorf("record proxy apply status: %w", err)
	}
	return nil
}
