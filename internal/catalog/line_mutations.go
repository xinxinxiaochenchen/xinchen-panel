package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func lineByID(ctx context.Context, tx pgx.Tx, lineID string) (Line, error) {
	line, err := scanLine(tx.QueryRow(ctx, lineSelect+` WHERE`+singleHopWhere+` AND l.id=$1`, lineID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Line{}, ErrNotFound
	}
	if err != nil {
		return Line{}, fmt.Errorf("load line: %w", err)
	}
	return line, nil
}

func (r *PostgresRepository) UpdateSharedLine(ctx context.Context, lineID string, patch LinePatch, actorID, requestID string) (Line, error) {
	return r.updateLine(ctx, lineID, patch, nil, actorID, requestID)
}

func (r *PostgresRepository) UpdateOwnLine(ctx context.Context, ownerID, lineID string, patch LinePatch, requestID string) (Line, error) {
	return r.updateLine(ctx, lineID, patch, &ownerID, ownerID, requestID)
}

func (r *PostgresRepository) updateLine(ctx context.Context, lineID string, patch LinePatch, expectedOwner *string, actorID, requestID string) (Line, error) {
	patch, err := NormalizeLinePatch(patch, expectedOwner == nil)
	if err != nil {
		return Line{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return Line{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Line{}, fmt.Errorf("begin line update: %w", err)
	}
	defer tx.Rollback(ctx)
	var ownerID *string
	if err := tx.QueryRow(ctx, `SELECT owner_user_id::text FROM lines WHERE id=$1 FOR UPDATE`, lineID).Scan(&ownerID); errors.Is(err, pgx.ErrNoRows) {
		return Line{}, ErrNotFound
	} else if err != nil {
		return Line{}, fmt.Errorf("lock line: %w", err)
	}
	if (expectedOwner == nil && ownerID != nil) || (expectedOwner != nil && (ownerID == nil || *ownerID != *expectedOwner)) {
		return Line{}, ErrNotFound
	}
	before, err := lineByID(ctx, tx, lineID)
	if err != nil {
		return Line{}, err
	}
	var membershipID string
	if expectedOwner != nil {
		var snapshotJSON []byte
		err := tx.QueryRow(ctx, `SELECT id::text,snapshot_json FROM memberships
WHERE user_id=$1 AND status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp() FOR SHARE`, *expectedOwner).Scan(&membershipID, &snapshotJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return Line{}, ErrNotFound
		}
		if err != nil {
			return Line{}, fmt.Errorf("find line membership: %w", err)
		}
		var snapshot lineGrantSnapshot
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			return Line{}, fmt.Errorf("decode line entitlement: %w", err)
		}
		var groupID string
		if err := tx.QueryRow(ctx, `SELECT group_id::text FROM nodes WHERE id=$1 FOR SHARE`, before.Hops[0].NodeID).Scan(&groupID); err != nil {
			return Line{}, fmt.Errorf("load line node group: %w", err)
		}
		if !snapshot.Limits.AllowCustomLines || snapshot.Limits.MaxHops != 1 || !slices.Contains(snapshot.ResourceGroupIDs, groupID) {
			return Line{}, ErrNotFound
		}
	}
	if patch.Enabled != nil && *patch.Enabled {
		_, err := lockUsableProxyNode(ctx, tx, before.Hops[0].NodeID)
		if err != nil {
			return Line{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE lines SET name=COALESCE($2,name),enabled=COALESCE($3,enabled),
priority=COALESCE($4,priority),weight=COALESCE($5,weight),
multiplier_milli=COALESCE($6,multiplier_milli),tags=COALESCE($7,tags),updated_at=now()
WHERE id=$1`, lineID, patch.Name, patch.Enabled, patch.Priority, patch.Weight,
		patch.MultiplierMilli, patch.Tags); err != nil {
		return Line{}, fmt.Errorf("update line: %w", catalogError(err))
	}
	after, err := lineByID(ctx, tx, lineID)
	if err != nil {
		return Line{}, err
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return Line{}, err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return Line{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id)
VALUES ($1,$2,'update','line',$3,$4,$5,$6)`, auditID, actorID, lineID, string(beforeJSON), string(afterJSON), requestID); err != nil {
		return Line{}, fmt.Errorf("audit line update: %w", err)
	}
	if membershipID != "" {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT ends_at>clock_timestamp() FROM memberships WHERE id=$1`, membershipID).Scan(&active); err != nil {
			return Line{}, fmt.Errorf("recheck line membership: %w", err)
		}
		if !active {
			return Line{}, ErrNotFound
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Line{}, fmt.Errorf("commit line update: %w", catalogError(err))
	}
	return after, nil
}

func (r *PostgresRepository) DeleteOwnLine(ctx context.Context, ownerID, lineID, requestID string) error {
	auditID, err := id.NewV7()
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin line deletion: %w", err)
	}
	defer tx.Rollback(ctx)
	var storedOwner string
	if err := tx.QueryRow(ctx, `SELECT owner_user_id::text FROM lines WHERE id=$1 AND owner_user_id=$2 FOR UPDATE`, lineID, ownerID).Scan(&storedOwner); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock owned line: %w", err)
	}
	before, err := lineByID(ctx, tx, lineID)
	if err != nil {
		return err
	}
	var referenced bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plan_line_grants WHERE line_id=$1)`, lineID).Scan(&referenced); err != nil {
		return fmt.Errorf("check line grants: %w", err)
	}
	if referenced {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM lines WHERE id=$1`, lineID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrConflict
		}
		return fmt.Errorf("delete owned line: %w", err)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,request_id)
VALUES ($1,$2,'delete','line',$3,$4,$5)`, auditID, ownerID, lineID, string(beforeJSON), requestID); err != nil {
		return fmt.Errorf("audit line deletion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit line deletion: %w", err)
	}
	return nil
}
