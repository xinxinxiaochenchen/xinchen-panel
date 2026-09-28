package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("catalog resource not found")
	ErrConflict = errors.New("catalog resource conflict")
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func catalogError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23503":
			return ErrNotFound
		}
	}
	return err
}

func (r *PostgresRepository) CreateGroup(ctx context.Context, input GroupInput, actorID, requestID string) (ResourceGroup, error) {
	groupID, err := id.NewV7()
	if err != nil {
		return ResourceGroup{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return ResourceGroup{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ResourceGroup{}, fmt.Errorf("begin group creation: %w", err)
	}
	defer tx.Rollback(ctx)
	group := ResourceGroup{ID: groupID, Code: input.Code, Name: input.Name, Region: input.Region, Enabled: input.Enabled}
	err = tx.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region,enabled) VALUES ($1,$2,$3,$4,$5) RETURNING created_at`,
		group.ID, group.Code, group.Name, group.Region, group.Enabled).Scan(&group.CreatedAt)
	if err != nil {
		return ResourceGroup{}, fmt.Errorf("insert resource group: %w", catalogError(err))
	}
	after, err := json.Marshal(group)
	if err != nil {
		return ResourceGroup{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,'create','resource_group',$3,$4,$5)`, auditID, actorID, group.ID, string(after), requestID); err != nil {
		return ResourceGroup{}, fmt.Errorf("audit resource group: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ResourceGroup{}, fmt.Errorf("commit resource group: %w", err)
	}
	return group, nil
}

func (r *PostgresRepository) ListGroups(ctx context.Context, limit int, afterCode string) ([]ResourceGroup, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text,code,name,region,enabled,created_at FROM resource_groups WHERE code > $2 ORDER BY code LIMIT $1`, limit, afterCode)
	if err != nil {
		return nil, fmt.Errorf("list resource groups: %w", err)
	}
	defer rows.Close()
	groups := make([]ResourceGroup, 0)
	for rows.Next() {
		var group ResourceGroup
		if err := rows.Scan(&group.ID, &group.Code, &group.Name, &group.Region, &group.Enabled, &group.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan resource group: %w", err)
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (r *PostgresRepository) CreateNode(ctx context.Context, input NodeInput, actorID, requestID string) (Node, error) {
	nodeID, err := id.NewV7()
	if err != nil {
		return Node{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return Node{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Node{}, fmt.Errorf("begin node creation: %w", err)
	}
	defer tx.Rollback(ctx)
	node := Node{ID: nodeID, GroupID: input.GroupID, Name: input.Name, Region: input.Region,
		Host: input.Host, PublicIP: input.PublicIP, ProxyPort: input.ProxyPort, RelayPort: input.RelayPort,
		Capabilities: input.Capabilities, BandwidthBPS: input.BandwidthBPS,
		MultiplierMilli: input.MultiplierMilli, Tags: input.Tags, Enabled: input.Enabled, AgentStatus: "unknown"}
	if err := tx.QueryRow(ctx, `SELECT code FROM resource_groups WHERE id=$1`, input.GroupID).Scan(&node.GroupCode); errors.Is(err, pgx.ErrNoRows) {
		return Node{}, ErrNotFound
	} else if err != nil {
		return Node{}, fmt.Errorf("find node group: %w", err)
	}
	err = tx.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,public_ip,proxy_port,relay_port,capabilities,bandwidth_bps,multiplier_milli,tags,enabled)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING created_at`,
		node.ID, node.GroupID, node.Name, node.Region, node.Host, node.PublicIP, node.ProxyPort, node.RelayPort,
		node.Capabilities, node.BandwidthBPS, node.MultiplierMilli, node.Tags, node.Enabled).Scan(&node.CreatedAt)
	if err != nil {
		return Node{}, fmt.Errorf("insert node: %w", catalogError(err))
	}
	after, err := json.Marshal(node)
	if err != nil {
		return Node{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,'create','node',$3,$4,$5)`, auditID, actorID, node.ID, string(after), requestID); err != nil {
		return Node{}, fmt.Errorf("audit node: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Node{}, fmt.Errorf("commit node: %w", err)
	}
	return node, nil
}

const nodeSelect = `SELECT n.id::text,n.group_id::text,g.code,n.name,n.region,n.host,host(n.public_ip),n.proxy_port,n.relay_port,
n.capabilities,n.bandwidth_bps,COALESCE(n.multiplier_milli,1000),n.tags,n.enabled,
CASE WHEN a.status='revoked' THEN 'revoked'
     WHEN a.status='online' AND a.last_seen_at >= now()-interval '45 seconds' THEN 'online'
     WHEN a.id IS NULL THEN 'unknown' ELSE 'offline' END,
a.last_seen_at,
CASE WHEN a.status='online' AND a.last_seen_at >= now()-interval '45 seconds'
          AND a.latency_observed_at >= now()-interval '45 seconds' THEN a.latency_ms END,
n.created_at
FROM nodes n JOIN resource_groups g ON g.id=n.group_id LEFT JOIN agents a ON a.node_id=n.id`

const allowedNodeWhere = ` WHERE n.enabled AND g.enabled AND EXISTS (
SELECT 1 FROM memberships m
WHERE m.user_id=$1 AND m.status='active' AND m.starts_at<=now() AND m.ends_at>now()
AND (m.snapshot_json->'resource_group_ids') ? n.group_id::text)`

func scanNode(row pgx.Row) (Node, error) {
	var node Node
	var publicIP pgtype.Text
	var proxyPort pgtype.Int4
	var relayPort pgtype.Int4
	var bandwidth pgtype.Int8
	var lastSeen pgtype.Timestamptz
	var latency pgtype.Int4
	err := row.Scan(&node.ID, &node.GroupID, &node.GroupCode, &node.Name, &node.Region,
		&node.Host, &publicIP, &proxyPort, &relayPort, &node.Capabilities, &bandwidth,
		&node.MultiplierMilli, &node.Tags, &node.Enabled, &node.AgentStatus,
		&lastSeen, &latency, &node.CreatedAt)
	if err != nil {
		return Node{}, err
	}
	if publicIP.Valid {
		value := publicIP.String
		node.PublicIP = &value
	}
	if proxyPort.Valid {
		value := int(proxyPort.Int32)
		node.ProxyPort = &value
	}
	if relayPort.Valid {
		value := int(relayPort.Int32)
		node.RelayPort = &value
	}
	if bandwidth.Valid {
		value := bandwidth.Int64
		node.BandwidthBPS = &value
	}
	if lastSeen.Valid {
		value := lastSeen.Time
		node.LastSeenAt = &value
	}
	if latency.Valid {
		value := int(latency.Int32)
		node.LatencyMS = &value
	}
	return node, nil
}

func collectNodes(rows pgx.Rows) ([]Node, error) {
	defer rows.Close()
	nodes := make([]Node, 0)
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	return nodes, nil
}

func (r *PostgresRepository) ListAllowedNodes(ctx context.Context, userID string, limit int, afterID string) ([]Node, error) {
	rows, err := r.pool.Query(ctx, nodeSelect+allowedNodeWhere+` AND n.id::text > $3 ORDER BY n.id::text LIMIT $2`, userID, limit, afterID)
	if err != nil {
		return nil, fmt.Errorf("list allowed nodes: %w", err)
	}
	return collectNodes(rows)
}

func (r *PostgresRepository) GetAllowedNode(ctx context.Context, userID, nodeID string) (Node, error) {
	node, err := scanNode(r.pool.QueryRow(ctx, nodeSelect+allowedNodeWhere+` AND n.id=$2`, userID, nodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("get allowed node: %w", err)
	}
	return node, nil
}

func (r *PostgresRepository) ListAllNodes(ctx context.Context, limit int, afterID string) ([]Node, error) {
	rows, err := r.pool.Query(ctx, nodeSelect+` WHERE n.id::text > $2 ORDER BY n.id::text LIMIT $1`, limit, afterID)
	if err != nil {
		return nil, fmt.Errorf("list all nodes: %w", err)
	}
	return collectNodes(rows)
}
