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

// UpdateNodeConfig replaces the mutable node connection metadata in one
// transaction. Operational enablement is intentionally handled by
// SetNodeEnabled so an address change cannot accidentally bring a node online.
// Database triggers reserve endpoint ports and invalidate relay certificates;
// the node.changed event then asks the convergence worker to republish config.
func (r *PostgresRepository) UpdateNodeConfig(ctx context.Context, nodeID string, input NodeInput, actorID, requestID string) (Node, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Node{}, fmt.Errorf("begin node configuration change: %w", err)
	}
	defer tx.Rollback(ctx)
	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM nodes WHERE id=$1 FOR UPDATE`, nodeID).Scan(&lockedID); errors.Is(err, pgx.ErrNoRows) {
		return Node{}, ErrNotFound
	} else if err != nil {
		return Node{}, fmt.Errorf("lock node configuration: %w", err)
	}
	before, err := scanNode(tx.QueryRow(ctx, nodeSelect+` WHERE n.id=$1`, nodeID))
	if err != nil {
		return Node{}, fmt.Errorf("read node before configuration update: %w", err)
	}
	if nodeConfigMatches(before, input) {
		return before, nil
	}
	var groupCode string
	if err := tx.QueryRow(ctx, `SELECT code FROM resource_groups WHERE id=$1`, input.GroupID).Scan(&groupCode); errors.Is(err, pgx.ErrNoRows) {
		return Node{}, ErrNotFound
	} else if err != nil {
		return Node{}, fmt.Errorf("find node resource group: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET group_id=$2,name=$3,region=$4,host=$5,public_ip=$6,proxy_port=$7,relay_port=$8,capabilities=$9,bandwidth_bps=$10,multiplier_milli=$11,tags=$12,updated_at=clock_timestamp() WHERE id=$1`,
		nodeID, input.GroupID, input.Name, input.Region, input.Host, input.PublicIP, input.ProxyPort, input.RelayPort,
		input.Capabilities, input.BandwidthBPS, input.MultiplierMilli, input.Tags); err != nil {
		return Node{}, fmt.Errorf("update node configuration: %w", catalogError(err))
	}
	after, err := scanNode(tx.QueryRow(ctx, nodeSelect+` WHERE n.id=$1`, nodeID))
	if err != nil {
		return Node{}, fmt.Errorf("read node after configuration update: %w", err)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return Node{}, err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return Node{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return Node{}, err
	}
	eventID, err := id.NewV7()
	if err != nil {
		return Node{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id)
VALUES ($1,$2,'update','node',$3,$4,$5,$6)`, auditID, actorID, nodeID, string(beforeJSON), string(afterJSON), requestID); err != nil {
		return Node{}, fmt.Errorf("audit node configuration: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
VALUES ($1,'node.changed',$2::uuid,jsonb_build_object('node_id',($2::uuid)::text),$3)`, eventID, nodeID, "node-config:"+eventID); err != nil {
		return Node{}, fmt.Errorf("queue node configuration convergence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Node{}, fmt.Errorf("commit node configuration: %w", err)
	}
	return after, nil
}

func nodeConfigMatches(current Node, input NodeInput) bool {
	return current.GroupID == input.GroupID && current.Name == input.Name &&
		current.Region == input.Region && current.Host == input.Host &&
		reflect.DeepEqual(current.PublicIP, input.PublicIP) &&
		reflect.DeepEqual(current.ProxyPort, input.ProxyPort) &&
		reflect.DeepEqual(current.RelayPort, input.RelayPort) &&
		reflect.DeepEqual(current.Capabilities, input.Capabilities) &&
		reflect.DeepEqual(current.BandwidthBPS, input.BandwidthBPS) &&
		current.MultiplierMilli == input.MultiplierMilli &&
		reflect.DeepEqual(current.Tags, input.Tags)
}

// SetNodeEnabled changes only the operational state. A durable outbox event
// asks the convergence worker to publish the resulting node configuration.
func (r *PostgresRepository) SetNodeEnabled(ctx context.Context, nodeID string, enabled bool, actorID, requestID string) (Node, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Node{}, fmt.Errorf("begin node status change: %w", err)
	}
	defer tx.Rollback(ctx)
	var current bool
	err = tx.QueryRow(ctx, `SELECT enabled FROM nodes WHERE id=$1 FOR UPDATE`, nodeID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("lock node: %w", err)
	}
	before, err := scanNode(tx.QueryRow(ctx, nodeSelect+` WHERE n.id=$1`, nodeID))
	if err != nil {
		return Node{}, fmt.Errorf("read node before update: %w", err)
	}
	if current == enabled {
		return before, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE nodes SET enabled=$2,updated_at=clock_timestamp() WHERE id=$1`, nodeID, enabled); err != nil {
		return Node{}, fmt.Errorf("update node status: %w", err)
	}
	after, err := scanNode(tx.QueryRow(ctx, nodeSelect+` WHERE n.id=$1`, nodeID))
	if err != nil {
		return Node{}, fmt.Errorf("read node after update: %w", err)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return Node{}, err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return Node{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return Node{}, err
	}
	eventID, err := id.NewV7()
	if err != nil {
		return Node{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id)
VALUES ($1,$2,'update','node',$3,$4,$5,$6)`, auditID, actorID, nodeID, string(beforeJSON), string(afterJSON), requestID); err != nil {
		return Node{}, fmt.Errorf("audit node status: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
VALUES ($1,'node.changed',$2::uuid,jsonb_build_object('node_id',($2::uuid)::text),$3)`, eventID, nodeID, "node-status:"+eventID); err != nil {
		return Node{}, fmt.Errorf("queue node status convergence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Node{}, fmt.Errorf("commit node status: %w", err)
	}
	return after, nil
}
