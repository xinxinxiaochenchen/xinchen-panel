package forward

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

func authorizeForwardLine(ctx context.Context, tx pgx.Tx, ownerID, ingressNodeID, lineID string, grant entitlementSnapshot) error {
	var lineOwner *string
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT owner_user_id::text,enabled FROM lines WHERE id=$1 FOR SHARE`, lineID).Scan(&lineOwner, &enabled); err != nil || !enabled {
		return ErrNotFound
	}
	if lineOwner == nil {
		if !slices.Contains(grant.LineIDs, lineID) {
			return ErrNotFound
		}
	} else if *lineOwner != ownerID || !grant.Limits.AllowCustomLines {
		return ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT h.position,h.node_id::text,h.role,n.group_id::text,n.enabled,g.enabled,n.relay_port,n.capabilities
FROM line_hops h JOIN nodes n ON n.id=h.node_id JOIN resource_groups g ON g.id=n.group_id
WHERE h.line_id=$1 ORDER BY h.position FOR SHARE OF n,g`, lineID)
	if err != nil {
		return fmt.Errorf("lock forward line hops: %w", err)
	}
	type hop struct {
		position                  int
		nodeID, role, groupID     string
		nodeEnabled, groupEnabled bool
		relayPort                 *int
		capabilities              []string
	}
	hops := make([]hop, 0, 8)
	for rows.Next() {
		var h hop
		if err := rows.Scan(&h.position, &h.nodeID, &h.role, &h.groupID, &h.nodeEnabled, &h.groupEnabled, &h.relayPort, &h.capabilities); err != nil {
			rows.Close()
			return fmt.Errorf("scan forward line hop: %w", err)
		}
		hops = append(hops, h)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read forward line hops: %w", err)
	}
	rows.Close()
	if len(hops) < 2 || len(hops) > 8 || len(hops) > grant.Limits.MaxHops || hops[0].nodeID != ingressNodeID {
		return ErrNotFound
	}
	for index, h := range hops {
		role := "relay"
		if index == 0 {
			role = "ingress"
		} else if index == len(hops)-1 {
			role = "egress"
		}
		if h.position != index || h.role != role || !h.nodeEnabled || !h.groupEnabled || h.relayPort == nil || !slices.Contains(h.capabilities, "forward") || !slices.Contains(grant.ResourceGroupIDs, h.groupID) {
			return ErrNotFound
		}
	}
	return nil
}
