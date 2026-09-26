package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNodeNotFound = errors.New("forward snapshot node not found")

type ForwardSnapshotRepository struct{ pool *pgxpool.Pool }

func NewForwardSnapshotRepository(pool *pgxpool.Pool) *ForwardSnapshotRepository {
	return &ForwardSnapshotRepository{pool: pool}
}

// Compile reads all source facts from one consistent PostgreSQL snapshot.
// It does not persist or publish a revision; the convergence service owns that step.
func (r *ForwardSnapshotRepository) Compile(ctx context.Context, nodeID string, revision uint64) (CompiledForwardSnapshot, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return CompiledForwardSnapshot{}, fmt.Errorf("begin forward snapshot read: %w", err)
	}
	defer tx.Rollback(ctx)
	node, err := readForwardNode(ctx, tx, nodeID)
	if err != nil {
		return CompiledForwardSnapshot{}, err
	}
	var rules []ForwardFacts
	if node.Enabled && node.GroupEnabled && node.ForwardCapable {
		rules, err = readForwardFacts(ctx, tx, nodeID)
		if err != nil {
			return CompiledForwardSnapshot{}, err
		}
	}
	snapshot, err := CompileForwardSnapshot(node, rules, revision)
	if err != nil {
		return CompiledForwardSnapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CompiledForwardSnapshot{}, fmt.Errorf("commit forward snapshot read: %w", err)
	}
	return snapshot, nil
}

func readForwardNode(ctx context.Context, tx pgx.Tx, nodeID string) (NodeFacts, error) {
	var node NodeFacts
	var capabilities []string
	err := tx.QueryRow(ctx, `SELECT n.id::text,n.group_id::text,n.enabled,g.enabled,n.capabilities,COALESCE(n.proxy_port,0)
FROM nodes n JOIN resource_groups g ON g.id=n.group_id WHERE n.id=$1`, nodeID).Scan(
		&node.ID, &node.GroupID, &node.Enabled, &node.GroupEnabled, &capabilities, &node.ProxyPort)
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeFacts{}, ErrNodeNotFound
	}
	if err != nil {
		return NodeFacts{}, fmt.Errorf("read forward node: %w", err)
	}
	node.ForwardCapable = slices.Contains(capabilities, "forward")
	node.ProxyCapable = slices.Contains(capabilities, "proxy")
	return node, nil
}

const forwardFactsQuery = `SELECT f.id::text,f.ingress_node_id::text,f.ingress_port,
COALESCE(f.target_node_id::text,''),COALESCE(nt.group_id::text,''),COALESCE(f.target_host,host(nt.public_ip),nt.host,''),
f.target_port,f.protocol,f.enabled,u.status='active',m.snapshot_json,
COALESCE(nt.enabled,false),COALESCE(gt.enabled,false),
EXISTS (SELECT 1 FROM forward_target_policies p
        WHERE p.enabled AND p.kind=CASE WHEN f.target_node_id IS NULL THEN 'public_host' ELSE 'node' END
        AND p.target_group_id IS NOT DISTINCT FROM nt.group_id AND p.protocol='TCP'
        AND p.port_start<=f.target_port AND p.port_end>=f.target_port),
EXISTS (SELECT 1 FROM forward_target_policies p
        WHERE p.enabled AND p.kind=CASE WHEN f.target_node_id IS NULL THEN 'public_host' ELSE 'node' END
        AND p.target_group_id IS NOT DISTINCT FROM nt.group_id AND p.protocol='UDP'
        AND p.port_start<=f.target_port AND p.port_end>=f.target_port)
FROM forward_rules f
JOIN users u ON u.id=f.user_id
LEFT JOIN nodes nt ON nt.id=f.target_node_id
LEFT JOIN resource_groups gt ON gt.id=nt.group_id
LEFT JOIN LATERAL (SELECT snapshot_json FROM memberships mm
                   WHERE mm.user_id=f.user_id AND mm.status='active'
                   AND mm.starts_at<=statement_timestamp() AND mm.ends_at>statement_timestamp()
                   LIMIT 1) m ON true
WHERE f.ingress_node_id=$1 AND f.enabled
ORDER BY f.id`

func readForwardFacts(ctx context.Context, tx pgx.Tx, nodeID string) ([]ForwardFacts, error) {
	rows, err := tx.Query(ctx, forwardFactsQuery, nodeID)
	if err != nil {
		return nil, fmt.Errorf("query forward snapshot facts: %w", err)
	}
	defer rows.Close()
	facts := make([]ForwardFacts, 0)
	for rows.Next() {
		var fact ForwardFacts
		var memberSnapshot []byte
		if err := rows.Scan(&fact.ID, &fact.IngressNodeID, &fact.IngressPort,
			&fact.TargetNodeID, &fact.TargetGroupID, &fact.TargetHost, &fact.TargetPort,
			&fact.Protocol, &fact.Enabled, &fact.OwnerActive, &memberSnapshot,
			&fact.TargetNodeEnabled, &fact.TargetGroupEnabled, &fact.TCPPolicyAllowed, &fact.UDPPolicyAllowed); err != nil {
			return nil, fmt.Errorf("scan forward snapshot facts: %w", err)
		}
		if memberSnapshot != nil {
			var grant struct {
				ResourceGroupIDs []string `json:"resource_group_ids"`
				Limits           struct {
					MaxForwardRulesPerNode int `json:"max_forward_rules_per_node"`
				} `json:"limits"`
			}
			if err := json.Unmarshal(memberSnapshot, &grant); err != nil {
				fact.EligibilityError = "invalid membership snapshot"
				facts = append(facts, fact)
				continue
			}
			fact.MembershipActive = true
			fact.MemberGroupIDs = grant.ResourceGroupIDs
			fact.MaxForwardRulesPerNode = grant.Limits.MaxForwardRulesPerNode
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read forward snapshot facts: %w", err)
	}
	return facts, nil
}
