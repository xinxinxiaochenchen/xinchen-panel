package subscription

import (
	"context"
	"controlplane/internal/entitlement"
	"errors"
	"github.com/jackc/pgx/v5"
	"slices"
)

func authorizeTargets(ctx context.Context, tx pgx.Tx, owner string, ids []string, grant entitlement.Snapshot) error {
	rows, err := tx.Query(ctx, `SELECT a.id::text,l.id::text,COALESCE(l.owner_user_id::text,''),
ARRAY(SELECT nh.group_id::text FROM line_hops lh JOIN nodes nh ON nh.id=lh.node_id WHERE lh.line_id=l.id ORDER BY lh.position)
FROM proxy_accesses a JOIN lines l ON l.id=a.line_id
JOIN line_hops h ON h.line_id=l.id AND h.position=0 AND h.role IN ('egress','ingress')
JOIN nodes n ON n.id=h.node_id JOIN resource_groups g ON g.id=n.group_id
WHERE a.user_id=$1 AND a.id=ANY($2::uuid[]) AND l.enabled AND n.enabled AND g.enabled
AND 'proxy'=ANY(n.capabilities) AND n.proxy_port IS NOT NULL
AND NOT EXISTS(SELECT 1 FROM line_hops h2 JOIN nodes n2 ON n2.id=h2.node_id
JOIN resource_groups g2 ON g2.id=n2.group_id WHERE h2.line_id=l.id AND
(NOT n2.enabled OR NOT g2.enabled OR
(h2.position>0 AND (n2.relay_port IS NULL OR NOT 'forward'=ANY(n2.capabilities))) OR
(h2.position=0 AND h.role='ingress' AND (n2.relay_port IS NULL OR NOT 'forward'=ANY(n2.capabilities) OR
n2.proxy_port IS NULL OR NOT 'proxy'=ANY(n2.capabilities)))))
ORDER BY a.id FOR SHARE OF a,l,n,g`, owner, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var access, line, lineOwner string
		var groups []string
		if err := rows.Scan(&access, &line, &lineOwner, &groups); err != nil {
			return err
		}
		if !lineAllowedHops(owner, line, lineOwner, groups, grant) {
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

func authorizeProfile(ctx context.Context, tx pgx.Tx, owner string, profileID *string) error {
	if profileID == nil {
		return nil
	}
	var exists bool
	err := tx.QueryRow(ctx, `SELECT true FROM routing_profiles WHERE id=$1 AND user_id=$2 AND enabled FOR SHARE`, *profileID, owner).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func lineAllowedHops(owner, line, lineOwner string, groups []string, grant entitlement.Snapshot) bool {
	if len(groups) == 0 || (len(groups) > 1 && len(groups) > grant.Limits.MaxHops) {
		return false
	}
	for _, group := range groups {
		if !slices.Contains(grant.ResourceGroupIDs, group) {
			return false
		}
	}
	if lineOwner == "" {
		return slices.Contains(grant.LineIDs, line)
	}
	return lineOwner == owner && grant.Limits.AllowCustomLines
}
