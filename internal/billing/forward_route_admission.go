package billing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func authorizeForwardRoute(ctx context.Context, tx pgx.Tx, lineID, periodID, ingressNodeID string) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT topology.hop_count BETWEEN 2 AND 8
AND topology.last_position=topology.hop_count-1
AND topology.hop_count<=COALESCE((p.snapshot_json->'limits'->>'max_hops')::int,1)
AND NOT EXISTS(SELECT 1 FROM line_hops step
 JOIN nodes n ON n.id=step.node_id JOIN resource_groups g ON g.id=n.group_id
 LEFT JOIN agents a ON a.node_id=n.id
 WHERE step.line_id=l.id AND (
   step.position>=topology.hop_count
   OR step.role<>CASE WHEN step.position=0 THEN 'ingress'
                     WHEN step.position=topology.hop_count-1 THEN 'egress' ELSE 'relay' END
   OR step.position=0 AND step.node_id<>$3::uuid
   OR NOT n.enabled OR NOT g.enabled OR n.relay_port IS NULL OR NOT 'forward'=ANY(n.capabilities)
   OR NOT (COALESCE(p.snapshot_json->'resource_group_ids','[]'::jsonb) ? n.group_id::text)
   OR a.status IS DISTINCT FROM 'online' OR a.last_seen_at<=clock_timestamp()-interval '45 seconds'
   OR a.desired_revision IS DISTINCT FROM a.applied_revision OR NOT 'relay'=ANY(a.capabilities)
   OR NOT (COALESCE(a.cert_expires_at>clock_timestamp(),false) OR EXISTS(SELECT 1 FROM agent_certificate_grants cg WHERE cg.node_id=n.id AND cg.expires_at>clock_timestamp()))
   OR NOT EXISTS(SELECT 1 FROM agent_relay_certificate_grants rg WHERE rg.node_id=n.id AND rg.expires_at>clock_timestamp())
   OR NOT EXISTS(SELECT 1 FROM config_revisions cr WHERE cr.node_id=n.id AND cr.revision=a.applied_revision AND cr.status='applied'
      AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(cr.payload_json->'relay_config','[]'::jsonb)) route
                 WHERE route->>'line_id'=l.id::text AND (route->>'generation')::bigint=l.relay_generation
                   AND route->>'role'=step.role))))
FROM lines l JOIN billing_periods p ON p.id=$2
JOIN LATERAL (SELECT count(*) AS hop_count,max(position) AS last_position FROM line_hops h WHERE h.line_id=l.id) topology ON true
WHERE l.id=$1 AND l.enabled`, lineID, periodID, ingressNodeID).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, databaseError(err)
	}
	return allowed, nil
}
