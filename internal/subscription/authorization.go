package subscription

import (
	"context"
	"controlplane/internal/entitlement"
	"github.com/jackc/pgx/v5"
	"slices"
)

func authorizeTargets(ctx context.Context, tx pgx.Tx, owner string, ids []string, grant entitlement.Snapshot) error {
	rows, err := tx.Query(ctx, `SELECT a.id::text,l.id::text,COALESCE(l.owner_user_id::text,''),n.group_id::text
FROM proxy_accesses a JOIN lines l ON l.id=a.line_id
JOIN line_hops h ON h.line_id=l.id AND h.position=0 AND h.role='egress'
JOIN nodes n ON n.id=h.node_id JOIN resource_groups g ON g.id=n.group_id
WHERE a.user_id=$1 AND a.id=ANY($2::uuid[]) AND l.enabled AND n.enabled AND g.enabled
AND 'proxy'=ANY(n.capabilities) AND n.proxy_port IS NOT NULL
AND NOT EXISTS(SELECT 1 FROM line_hops h2 WHERE h2.line_id=l.id AND h2.position<>0)
ORDER BY a.id FOR SHARE OF a,l,n,g`, owner, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var access, line, lineOwner, group string
		if err := rows.Scan(&access, &line, &lineOwner, &group); err != nil {
			return err
		}
		if !lineAllowed(owner, line, lineOwner, group, grant) {
			return ErrNotFound
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(ids) {
		return ErrNotFound
	}
	return nil
}
func lineAllowed(owner, line, lineOwner, group string, grant entitlement.Snapshot) bool {
	if !slices.Contains(grant.ResourceGroupIDs, group) {
		return false
	}
	if lineOwner == "" {
		return slices.Contains(grant.LineIDs, line)
	}
	return lineOwner == owner && grant.Limits.AllowCustomLines
}
