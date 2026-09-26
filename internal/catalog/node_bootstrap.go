package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

// EnsureGroupAndNode provisions the first catalog node as one audited
// transaction. A matching group/name pair is idempotent; changed identity
// fields are rejected instead of creating a second node.
func (r *PostgresRepository) EnsureGroupAndNode(ctx context.Context, groupID string, group GroupInput, input NodeInput, actorID, requestID string) (ResourceGroup, Node, bool, error) {
	if groupID == "" || input.GroupID != groupID {
		return ResourceGroup{}, Node{}, false, errors.New("bootstrap group and node IDs do not match")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ResourceGroup{}, Node{}, false, fmt.Errorf("begin catalog bootstrap: %w", err)
	}
	defer tx.Rollback(ctx)
	// Serialize bootstrap attempts for a group before checking whether it exists.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, group.Code); err != nil {
		return ResourceGroup{}, Node{}, false, fmt.Errorf("lock bootstrap resource group: %w", err)
	}
	groupAuditID, err := id.NewV7()
	if err != nil {
		return ResourceGroup{}, Node{}, false, err
	}
	var resourceGroup ResourceGroup
	err = tx.QueryRow(ctx, `SELECT id::text,code,name,region,enabled,created_at FROM resource_groups WHERE code=$1 FOR UPDATE`, group.Code).
		Scan(&resourceGroup.ID, &resourceGroup.Code, &resourceGroup.Name, &resourceGroup.Region, &resourceGroup.Enabled, &resourceGroup.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		resourceGroup = ResourceGroup{ID: groupID, Code: group.Code, Name: group.Name, Region: group.Region, Enabled: group.Enabled}
		if err := tx.QueryRow(ctx, `INSERT INTO resource_groups(id,code,name,region,enabled) VALUES ($1,$2,$3,$4,$5) RETURNING created_at`, resourceGroup.ID, resourceGroup.Code, resourceGroup.Name, resourceGroup.Region, resourceGroup.Enabled).Scan(&resourceGroup.CreatedAt); err != nil {
			return ResourceGroup{}, Node{}, false, fmt.Errorf("insert bootstrap resource group: %w", catalogError(err))
		}
		after, err := json.Marshal(resourceGroup)
		if err != nil {
			return ResourceGroup{}, Node{}, false, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id) VALUES ($1,$2,'create','resource_group',$3,$4,$5)`, groupAuditID, actorID, resourceGroup.ID, string(after), requestID); err != nil {
			return ResourceGroup{}, Node{}, false, fmt.Errorf("audit bootstrap resource group: %w", err)
		}
	} else if err != nil {
		return ResourceGroup{}, Node{}, false, fmt.Errorf("find bootstrap resource group: %w", err)
	}
	if !resourceGroup.Enabled {
		return ResourceGroup{}, Node{}, false, ErrConflict
	}
	if resourceGroup.Name != group.Name || resourceGroup.Region != group.Region {
		return ResourceGroup{}, Node{}, false, ErrConflict
	}
	input.GroupID = resourceGroup.ID
	existing, err := scanNode(tx.QueryRow(ctx, nodeSelect+` WHERE n.group_id=$1 AND n.name=$2`, resourceGroup.ID, input.Name))
	if err == nil {
		if existing.Region != input.Region || existing.Host != input.Host || !reflect.DeepEqual(existing.PublicIP, input.PublicIP) ||
			!reflect.DeepEqual(existing.ProxyPort, input.ProxyPort) || !reflect.DeepEqual(existing.Capabilities, input.Capabilities) ||
			!reflect.DeepEqual(existing.BandwidthBPS, input.BandwidthBPS) || existing.MultiplierMilli != input.MultiplierMilli ||
			!reflect.DeepEqual(existing.Tags, input.Tags) || existing.Enabled != input.Enabled {
			return ResourceGroup{}, Node{}, false, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return ResourceGroup{}, Node{}, false, fmt.Errorf("commit existing bootstrap node: %w", err)
		}
		return resourceGroup, existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ResourceGroup{}, Node{}, false, fmt.Errorf("find bootstrap node: %w", err)
	}
	nodeID, err := id.NewV7()
	if err != nil {
		return ResourceGroup{}, Node{}, false, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return ResourceGroup{}, Node{}, false, err
	}
	node := Node{ID: nodeID, GroupID: resourceGroup.ID, GroupCode: resourceGroup.Code, Name: input.Name, Region: input.Region, Host: input.Host, PublicIP: input.PublicIP, ProxyPort: input.ProxyPort, Capabilities: input.Capabilities, BandwidthBPS: input.BandwidthBPS, MultiplierMilli: input.MultiplierMilli, Tags: input.Tags, Enabled: input.Enabled, AgentStatus: "unknown"}
	if err := tx.QueryRow(ctx, `INSERT INTO nodes(id,group_id,name,region,host,public_ip,proxy_port,capabilities,bandwidth_bps,multiplier_milli,tags,enabled) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING created_at`, node.ID, node.GroupID, node.Name, node.Region, node.Host, node.PublicIP, node.ProxyPort, node.Capabilities, node.BandwidthBPS, node.MultiplierMilli, node.Tags, node.Enabled).Scan(&node.CreatedAt); err != nil {
		return ResourceGroup{}, Node{}, false, fmt.Errorf("insert bootstrap node: %w", catalogError(err))
	}
	after, err := json.Marshal(node)
	if err != nil {
		return ResourceGroup{}, Node{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id) VALUES ($1,$2,'create','node',$3,$4,$5)`, auditID, actorID, node.ID, string(after), requestID); err != nil {
		return ResourceGroup{}, Node{}, false, fmt.Errorf("audit bootstrap node: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ResourceGroup{}, Node{}, false, fmt.Errorf("commit catalog bootstrap: %w", err)
	}
	return resourceGroup, node, true, nil
}
