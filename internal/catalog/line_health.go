package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LineHealth describes the current control-plane view of a line. It never
// includes Agent certificates, route secrets, or proxy credentials.
type LineHealth struct {
	LineID     string          `json:"line_id"`
	State      string          `json:"state"`
	Reason     string          `json:"reason"`
	Generation int64           `json:"generation"`
	Hops       []LineHopHealth `json:"hops"`
}

type LineHopHealth struct {
	Position              int    `json:"position"`
	NodeID                string `json:"node_id"`
	Role                  string `json:"role"`
	NodeEnabled           bool   `json:"node_enabled"`
	GroupEnabled          bool   `json:"group_enabled"`
	ProxyCapable          bool   `json:"proxy_capable"`
	RelayCapable          bool   `json:"relay_capable"`
	ProxyPortReady        bool   `json:"proxy_port_ready"`
	RelayPortReady        bool   `json:"relay_port_ready"`
	AgentOnline           bool   `json:"agent_online"`
	CertificateReady      bool   `json:"certificate_ready"`
	RelayCertificateReady bool   `json:"relay_certificate_ready"`
	DesiredRevision       int64  `json:"desired_revision"`
	AppliedRevision       int64  `json:"applied_revision"`
	RelayApplied          bool   `json:"relay_applied"`
}

func evaluateLineHealth(enabled bool, hops []LineHopHealth) (string, string) {
	if !enabled {
		return "disabled", "line_disabled"
	}
	if len(hops) == 0 || len(hops) > 8 {
		return "unavailable", "invalid_topology"
	}
	for i, hop := range hops {
		expectedRole := "relay"
		if i == 0 && len(hops) > 1 {
			expectedRole = "ingress"
		}
		if i == len(hops)-1 {
			expectedRole = "egress"
		}
		if hop.Position != i || hop.Role != expectedRole {
			return "unavailable", "invalid_topology"
		}
		if !hop.NodeEnabled || !hop.GroupEnabled {
			return "unavailable", "node_disabled"
		}
		if !hop.AgentOnline {
			return "unavailable", "agent_offline"
		}
		if i == 0 && (!hop.ProxyCapable || !hop.ProxyPortReady) {
			return "unavailable", "proxy_unavailable"
		}
		if len(hops) > 1 && (!hop.RelayCapable || !hop.RelayPortReady) {
			return "unavailable", "relay_unavailable"
		}
		if !hop.CertificateReady || len(hops) > 1 && !hop.RelayCertificateReady {
			return "unavailable", "certificate_unavailable"
		}
	}
	if len(hops) > 1 {
		for _, hop := range hops {
			if !hop.RelayApplied || hop.DesiredRevision != hop.AppliedRevision {
				return "converging", "relay_pending"
			}
		}
	} else if hops[0].DesiredRevision != hops[0].AppliedRevision {
		return "converging", "config_pending"
	}
	return "ready", ""
}

func (r *PostgresRepository) GetLineHealth(ctx context.Context, lineID string) (LineHealth, error) {
	return r.getLineHealth(ctx, "", lineID)
}

func (r *PostgresRepository) GetAllowedLineHealth(ctx context.Context, userID, lineID string) (LineHealth, error) {
	return r.getLineHealth(ctx, userID, lineID)
}

func (r *PostgresRepository) getLineHealth(ctx context.Context, userID, lineID string) (LineHealth, error) {
	if !ValidID(lineID) || userID != "" && !ValidID(userID) {
		return LineHealth{}, ErrNotFound
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return LineHealth{}, fmt.Errorf("begin line health read: %w", err)
	}
	defer tx.Rollback(ctx)
	var line Line
	if userID == "" {
		line, err = scanLine(tx.QueryRow(ctx, lineSelect+` WHERE`+validLineWhere+` AND l.id=$1`, lineID))
	} else {
		line, err = scanLine(tx.QueryRow(ctx, lineSelect+allowedLineWhere+` AND l.id=$2`, userID, lineID))
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return LineHealth{}, ErrNotFound
	}
	if err != nil {
		return LineHealth{}, fmt.Errorf("authorize line health: %w", err)
	}
	result := LineHealth{LineID: line.ID, Hops: make([]LineHopHealth, 0, len(line.Hops))}
	if len(line.Hops) > 1 {
		if err := tx.QueryRow(ctx, `SELECT relay_generation FROM lines WHERE id=$1`, line.ID).Scan(&result.Generation); err != nil {
			return LineHealth{}, fmt.Errorf("read relay generation: %w", err)
		}
	}
	rows, err := tx.Query(ctx, `SELECT h.position,h.node_id::text,h.role,n.enabled,g.enabled,
COALESCE('proxy'=ANY(n.capabilities) AND 'proxy'=ANY(a.capabilities),false),
COALESCE('forward'=ANY(n.capabilities) AND 'relay'=ANY(a.capabilities),false),
n.proxy_port IS NOT NULL,n.relay_port IS NOT NULL,
COALESCE(a.status='online' AND a.last_seen_at>clock_timestamp()-interval '45 seconds',false),
COALESCE((a.cert_fingerprint IS NOT NULL AND a.cert_expires_at>clock_timestamp()) OR EXISTS(
SELECT 1 FROM agent_certificate_grants cg WHERE cg.node_id=n.id AND cg.expires_at>clock_timestamp()),false),
EXISTS(SELECT 1 FROM agent_relay_certificate_grants rg WHERE rg.node_id=n.id AND rg.expires_at>clock_timestamp()),
COALESCE(a.desired_revision,0),COALESCE(a.applied_revision,0),
COALESCE(EXISTS(SELECT 1 FROM config_revisions cr WHERE cr.node_id=n.id AND cr.revision=a.applied_revision
AND cr.status='applied' AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(cr.payload_json->'relay_config','[]'::jsonb)) item
WHERE item->>'line_id'=h.line_id::text AND item->>'generation'=l.relay_generation::text)),false)
FROM lines l JOIN line_hops h ON h.line_id=l.id JOIN nodes n ON n.id=h.node_id
JOIN resource_groups g ON g.id=n.group_id LEFT JOIN agents a ON a.node_id=n.id
WHERE l.id=$1 ORDER BY h.position`, line.ID)
	if err != nil {
		return LineHealth{}, fmt.Errorf("query line hop health: %w", err)
	}
	for rows.Next() {
		var hop LineHopHealth
		if err := rows.Scan(&hop.Position, &hop.NodeID, &hop.Role, &hop.NodeEnabled, &hop.GroupEnabled, &hop.ProxyCapable,
			&hop.RelayCapable, &hop.ProxyPortReady, &hop.RelayPortReady, &hop.AgentOnline, &hop.CertificateReady,
			&hop.RelayCertificateReady, &hop.DesiredRevision, &hop.AppliedRevision, &hop.RelayApplied); err != nil {
			rows.Close()
			return LineHealth{}, fmt.Errorf("scan line hop health: %w", err)
		}
		result.Hops = append(result.Hops, hop)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return LineHealth{}, fmt.Errorf("read line hop health: %w", err)
	}
	result.State, result.Reason = evaluateLineHealth(line.Enabled, result.Hops)
	if err := tx.Commit(ctx); err != nil {
		return LineHealth{}, fmt.Errorf("commit line health read: %w", err)
	}
	return result, nil
}
