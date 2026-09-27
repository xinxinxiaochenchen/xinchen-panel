package proxyaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

func (r *PostgresRepository) RevealOwn(ctx context.Context, ownerID, accessID string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := lockActiveProxyOwner(ctx, tx, ownerID); err != nil {
		return "", err
	}
	access, err := scanAccess(tx.QueryRow(ctx, accessSelect+` WHERE id=$1 AND user_id=$2 FOR SHARE`, accessID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find proxy credential: %w", err)
	}
	if _, err := authorizeAvailableProxyAccessLine(ctx, tx, ownerID, access.LineIDs); err != nil {
		return "", err
	}
	var ciphertext string
	err = tx.QueryRow(ctx, `SELECT credential_ciphertext FROM proxy_accesses WHERE id=$1 AND user_id=$2 FOR SHARE`, accessID, ownerID).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read proxy credential: %w", err)
	}
	credential, err := r.cipher.Open(accessID, ownerID, ciphertext)
	if err != nil {
		return "", fmt.Errorf("decrypt proxy credential: %w", err)
	}
	return credential, nil
}

func (r *PostgresRepository) RotateOwn(ctx context.Context, ownerID, accessID, requestID string) (string, error) {
	credential, err := GenerateCredential()
	if err != nil {
		return "", err
	}
	sealed, err := r.cipher.Seal(accessID, ownerID, credential)
	if err != nil {
		return "", err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := lockActiveProxyOwner(ctx, tx, ownerID); err != nil {
		return "", err
	}
	before, err := scanAccess(tx.QueryRow(ctx, accessSelect+` WHERE id=$1 AND user_id=$2 FOR UPDATE`, accessID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find proxy access for rotation: %w", err)
	}
	membershipID, err := authorizeAvailableProxyAccessLine(ctx, tx, ownerID, before.LineIDs)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE proxy_accesses SET credential_hash=$3,credential_ciphertext=$4,
apply_status='pending',updated_at=clock_timestamp() WHERE id=$1 AND user_id=$2`, accessID, ownerID,
		TrojanDigest(credential), sealed); err != nil {
		return "", fmt.Errorf("rotate proxy access: %w", proxyDatabaseError(err))
	}
	if err := recordAccessChange(ctx, tx, before, ownerID, "rotate", requestID); err != nil {
		return "", err
	}
	if err := recheckAccessMembership(ctx, tx, membershipID); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit proxy rotation: %w", proxyDatabaseError(err))
	}
	return credential, nil
}

func (r *PostgresRepository) UpdateOwn(ctx context.Context, ownerID, accessID string, patch AccessPatch, requestID string) (Access, error) {
	patch, err := NormalizeAccessPatch(patch)
	if err != nil {
		return Access{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Access{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockActiveProxyOwner(ctx, tx, ownerID); err != nil {
		return Access{}, err
	}
	before, err := scanAccess(tx.QueryRow(ctx, accessSelect+` WHERE id=$1 AND user_id=$2 FOR UPDATE`, accessID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Access{}, ErrNotFound
	}
	if err != nil {
		return Access{}, err
	}
	var membershipID string
	lineIDs := before.LineIDs
	lineOptions := before.LineOptions
	if patch.LineIDs != nil {
		lineIDs = append([]string(nil), (*patch.LineIDs)...)
		lineOptions = append([]LineOption(nil), patch.LineOptions...)
		var oldIngress, newIngress string
		if err := tx.QueryRow(ctx, `SELECT node_id::text FROM line_hops WHERE line_id=$1 AND position=0`, before.LineID).Scan(&oldIngress); err != nil {
			return Access{}, fmt.Errorf("read current proxy ingress: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT node_id::text FROM line_hops WHERE line_id=$1 AND position=0`, lineIDs[0]).Scan(&newIngress); err != nil {
			return Access{}, fmt.Errorf("read replacement proxy ingress: %w", err)
		}
		if oldIngress != newIngress {
			return Access{}, ValidationError{"line_ids", "candidate replacement cannot move the proxy ingress node"}
		}
		removed := make([]string, 0)
		for _, oldLine := range before.LineIDs {
			if !slices.Contains(lineIDs, oldLine) {
				removed = append(removed, oldLine)
			}
		}
		if len(removed) > 0 {
			var active bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM usage_sessions s
JOIN connection_lease_bindings b ON b.connection_id=s.id
JOIN quota_leases q ON q.id=b.lease_id
WHERE s.resource_kind='proxy' AND s.resource_id=$1 AND s.line_id=ANY($2::uuid[])
AND q.state='active' AND q.expires_at>clock_timestamp())`, accessID, removed).Scan(&active); err != nil {
				return Access{}, fmt.Errorf("check active proxy candidate sessions: %w", err)
			}
			if active {
				return Access{}, ErrConflict
			}
		}
	}
	if patch.LineIDs != nil {
		membershipID, err = authorizeProxyAccessLines(ctx, tx, ownerID, lineIDs)
		if err != nil {
			return Access{}, err
		}
	} else if patch.Enabled != nil && *patch.Enabled {
		membershipID, err = authorizeAvailableProxyAccessLine(ctx, tx, ownerID, lineIDs)
		if err != nil {
			return Access{}, err
		}
	}
	name, enabled := before.Name, before.Enabled
	if patch.Name != nil {
		name = *patch.Name
	}
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	if _, err := tx.Exec(ctx, `UPDATE proxy_accesses SET line_id=$3,name=$4,enabled=$5,
apply_status='pending',updated_at=clock_timestamp() WHERE id=$1 AND user_id=$2`, accessID, ownerID, lineIDs[0], name, enabled); err != nil {
		return Access{}, fmt.Errorf("update proxy access: %w", proxyDatabaseError(err))
	}
	if patch.LineIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM proxy_access_lines WHERE proxy_access_id=$1`, accessID); err != nil {
			return Access{}, fmt.Errorf("replace proxy access lines: %w", proxyDatabaseError(err))
		}
		for position, option := range lineOptions {
			if _, err := tx.Exec(ctx, `INSERT INTO proxy_access_lines(proxy_access_id,line_id,position,priority,weight)
VALUES($1,$2,$3,$4,$5)`, accessID, option.LineID, position, option.Priority, option.Weight); err != nil {
				return Access{}, fmt.Errorf("insert replacement proxy access line: %w", proxyDatabaseError(err))
			}
		}
	}
	after, err := scanAccess(tx.QueryRow(ctx, accessSelect+` WHERE id=$1 AND user_id=$2`, accessID, ownerID))
	if err != nil {
		return Access{}, fmt.Errorf("read updated proxy access: %w", proxyDatabaseError(err))
	}
	if err := recordAccessChange(ctx, tx, after, ownerID, "update", requestID); err != nil {
		return Access{}, err
	}
	if membershipID != "" {
		if err := recheckAccessMembership(ctx, tx, membershipID); err != nil {
			return Access{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Access{}, fmt.Errorf("commit proxy access update: %w", proxyDatabaseError(err))
	}
	return after, nil
}

func (r *PostgresRepository) DeleteOwn(ctx context.Context, ownerID, accessID, requestID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedOwnerID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, ownerID).Scan(&lockedOwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock proxy access owner for deletion: %w", err)
	}
	before, err := scanAccess(tx.QueryRow(ctx, accessSelect+` WHERE id=$1 AND user_id=$2 FOR UPDATE`, accessID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM proxy_accesses WHERE id=$1 AND user_id=$2`, accessID, ownerID); err != nil {
		return fmt.Errorf("delete proxy access: %w", proxyDatabaseError(err))
	}
	if err := recordAccessChange(ctx, tx, before, ownerID, "delete", requestID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit proxy access deletion: %w", proxyDatabaseError(err))
	}
	return nil
}

func recheckAccessMembership(ctx context.Context, tx pgx.Tx, membershipID string) error {
	var active bool
	if err := tx.QueryRow(ctx, `SELECT status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp()
FROM memberships WHERE id=$1`, membershipID).Scan(&active); err != nil {
		return fmt.Errorf("recheck proxy access membership: %w", err)
	}
	if !active {
		return ErrNotFound
	}
	return nil
}

func lockActiveProxyOwner(ctx context.Context, tx pgx.Tx, ownerID string) error {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, ownerID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || status != "active" && err == nil {
		return ErrNotFound
	}
	return err
}

// Existing pools can remain usable when their default line is disabled but an
// authorized fallback is ready. New or replacement pools use the stricter
// all-candidate check above.
func authorizeAvailableProxyAccessLine(ctx context.Context, tx pgx.Tx, ownerID string, lineIDs []string) (string, error) {
	for _, lineID := range lineIDs {
		membershipID, err := authorizeProxyLine(ctx, tx, ownerID, lineID)
		if err == nil {
			return membershipID, nil
		}
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrUnauthorized) {
			return "", err
		}
	}
	return "", ErrNotFound
}

func recordAccessChange(ctx context.Context, tx pgx.Tx, access Access, ownerID, action, requestID string) error {
	auditID, err := id.NewV7()
	if err != nil {
		return err
	}
	value, err := json.Marshal(access)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,$3,'proxy_access',$4,$5,$6)`, auditID, ownerID, action, access.ID, value, requestID); err != nil {
		return fmt.Errorf("audit proxy access change: %w", err)
	}
	lineIDs := access.LineIDs
	if len(lineIDs) == 0 {
		lineIDs = []string{access.LineID}
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT h.node_id::text FROM line_hops h
WHERE h.line_id=ANY($1::uuid[]) AND h.position=0`, lineIDs)
	if err != nil {
		return fmt.Errorf("resolve proxy access Agent nodes: %w", err)
	}
	nodeIDs := make([]string, 0, len(lineIDs))
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			rows.Close()
			return fmt.Errorf("scan proxy access Agent node: %w", err)
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("iterate proxy access Agent nodes: %w", err)
	}
	if len(nodeIDs) == 0 {
		return ErrNotFound
	}
	for _, nodeID := range nodeIDs {
		payload, err := json.Marshal(struct {
			NodeID string `json:"node_id"`
		}{NodeID: nodeID})
		if err != nil {
			return err
		}
		eventID, err := id.NewV7()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(id,kind,aggregate_id,payload,idempotency_key)
VALUES ($1,'proxy_access.changed',$2,$3,$4)`, eventID, access.ID, payload, eventID); err != nil {
			return fmt.Errorf("enqueue proxy access convergence: %w", err)
		}
	}
	return nil
}
