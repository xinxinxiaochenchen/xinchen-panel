package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrLimitReached = errors.New("catalog limit reached")

type lineGrantSnapshot struct {
	ResourceGroupIDs []string `json:"resource_group_ids"`
	LineIDs          []string `json:"line_ids"`
	Limits           struct {
		AllowCustomLines bool `json:"allow_custom_lines"`
		MaxCustomLines   int  `json:"max_custom_lines"`
		MaxHops          int  `json:"max_hops"`
	} `json:"limits"`
}

func lockUsableProxyNode(ctx context.Context, tx pgx.Tx, nodeID string) (string, error) {
	var groupID string
	var nodeEnabled, groupEnabled bool
	var capabilities []string
	var proxyPort pgtype.Int4
	err := tx.QueryRow(ctx, `SELECT n.group_id::text,n.enabled,g.enabled,n.capabilities,n.proxy_port
FROM nodes n JOIN resource_groups g ON g.id=n.group_id WHERE n.id=$1 FOR SHARE OF n,g`, nodeID).Scan(
		&groupID, &nodeEnabled, &groupEnabled, &capabilities, &proxyPort)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock line node: %w", err)
	}
	if !nodeEnabled || !groupEnabled || !proxyPort.Valid || !slices.Contains(capabilities, "proxy") {
		return "", ErrNotFound
	}
	return groupID, nil
}

func (r *PostgresRepository) CreateSharedLine(ctx context.Context, input LineInput, actorID, requestID string) (Line, error) {
	return r.createLine(ctx, input, nil, actorID, requestID)
}

func (r *PostgresRepository) CreateCustomLine(ctx context.Context, input LineInput, ownerID, requestID string) (Line, error) {
	if input.MultiplierMilli != nil {
		return Line{}, ValidationError{"multiplier_milli", "member lines cannot override billing multiplier"}
	}
	return r.createLine(ctx, input, &ownerID, ownerID, requestID)
}

func (r *PostgresRepository) createLine(ctx context.Context, input LineInput, ownerID *string, actorID, requestID string) (Line, error) {
	lineID, err := id.NewV7()
	if err != nil {
		return Line{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return Line{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Line{}, fmt.Errorf("begin line creation: %w", err)
	}
	defer tx.Rollback(ctx)
	var snapshot lineGrantSnapshot
	var membershipID string
	if ownerID != nil {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, *ownerID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
			return Line{}, ErrNotFound
		} else if err != nil {
			return Line{}, fmt.Errorf("lock line owner: %w", err)
		}
		if status != "active" {
			return Line{}, ErrNotFound
		}
		var snapshotJSON []byte
		err := tx.QueryRow(ctx, `SELECT id::text,snapshot_json FROM memberships
WHERE user_id=$1 AND status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp() FOR SHARE`, *ownerID).Scan(&membershipID, &snapshotJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return Line{}, ErrNotFound
		}
		if err != nil {
			return Line{}, fmt.Errorf("find line membership: %w", err)
		}
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
			return Line{}, fmt.Errorf("decode line entitlement: %w", err)
		}
		if !snapshot.Limits.AllowCustomLines || snapshot.Limits.MaxHops < 1 {
			return Line{}, ErrNotFound
		}
	}
	groupID, err := lockUsableProxyNode(ctx, tx, input.NodeID)
	if err != nil {
		return Line{}, err
	}
	if ownerID != nil {
		if !slices.Contains(snapshot.ResourceGroupIDs, groupID) {
			return Line{}, ErrNotFound
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM lines WHERE owner_user_id=$1`, *ownerID).Scan(&count); err != nil {
			return Line{}, fmt.Errorf("count owner lines: %w", err)
		}
		if count >= snapshot.Limits.MaxCustomLines {
			return Line{}, ErrLimitReached
		}
	}
	line := Line{ID: lineID, OwnerUserID: ownerID, Name: input.Name, Enabled: input.Enabled,
		Priority: input.Priority, Weight: input.Weight, MultiplierMilli: input.MultiplierMilli,
		Tags: input.Tags, Hops: []LineHop{{Position: 0, NodeID: input.NodeID, Role: "egress"}}}
	err = tx.QueryRow(ctx, `INSERT INTO lines(id,owner_user_id,name,enabled,priority,weight,multiplier_milli,tags,created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at,updated_at`, line.ID, line.OwnerUserID,
		line.Name, line.Enabled, line.Priority, line.Weight, line.MultiplierMilli, line.Tags, actorID).Scan(&line.CreatedAt, &line.UpdatedAt)
	if err != nil {
		return Line{}, fmt.Errorf("insert line: %w", catalogError(err))
	}
	if _, err := tx.Exec(ctx, `INSERT INTO line_hops(line_id,position,node_id,role) VALUES ($1,0,$2,'egress')`, line.ID, input.NodeID); err != nil {
		return Line{}, fmt.Errorf("insert line hop: %w", catalogError(err))
	}
	after, err := json.Marshal(line)
	if err != nil {
		return Line{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id)
VALUES ($1,$2,'create','line',$3,$4,$5)`, auditID, actorID, line.ID, string(after), requestID); err != nil {
		return Line{}, fmt.Errorf("audit line creation: %w", err)
	}
	if membershipID != "" {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp()
FROM memberships WHERE id=$1`, membershipID).Scan(&active); err != nil {
			return Line{}, fmt.Errorf("recheck line membership: %w", err)
		}
		if !active {
			return Line{}, ErrNotFound
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Line{}, fmt.Errorf("commit line creation: %w", catalogError(err))
	}
	return line, nil
}

const lineSelect = `SELECT l.id::text,l.owner_user_id::text,l.name,l.enabled,l.priority,l.weight,l.multiplier_milli,
l.tags,l.created_at,l.updated_at,h.node_id::text
FROM lines l JOIN line_hops h ON h.line_id=l.id AND h.position=0 AND h.role='egress'
JOIN nodes n ON n.id=h.node_id JOIN resource_groups g ON g.id=n.group_id`

const singleHopWhere = ` NOT EXISTS (SELECT 1 FROM line_hops extra WHERE extra.line_id=l.id AND extra.position<>0)`

const allowedLineWhere = ` WHERE` + singleHopWhere + ` AND EXISTS (SELECT 1 FROM memberships m
WHERE m.user_id=$1 AND m.status='active' AND m.starts_at<=now() AND m.ends_at>now()
AND (m.snapshot_json->'resource_group_ids') ? n.group_id::text
AND ((l.owner_user_id=$1 AND (m.snapshot_json #>> '{limits,allow_custom_lines}')::boolean=true)
OR (l.owner_user_id IS NULL AND l.enabled AND n.enabled AND g.enabled AND n.proxy_port IS NOT NULL
AND 'proxy'=ANY(n.capabilities) AND (m.snapshot_json->'line_ids') ? l.id::text)))`

func scanLine(row pgx.Row) (Line, error) {
	var line Line
	var multiplier pgtype.Int4
	var nodeID string
	if err := row.Scan(&line.ID, &line.OwnerUserID, &line.Name, &line.Enabled,
		&line.Priority, &line.Weight, &multiplier, &line.Tags, &line.CreatedAt, &line.UpdatedAt, &nodeID); err != nil {
		return Line{}, err
	}
	if multiplier.Valid {
		value := int(multiplier.Int32)
		line.MultiplierMilli = &value
	}
	line.Hops = []LineHop{{Position: 0, NodeID: nodeID, Role: "egress"}}
	return line, nil
}

func collectLines(rows pgx.Rows) ([]Line, error) {
	defer rows.Close()
	lines := make([]Line, 0)
	for rows.Next() {
		line, err := scanLine(rows)
		if err != nil {
			return nil, fmt.Errorf("scan line: %w", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lines: %w", err)
	}
	return lines, nil
}

func (r *PostgresRepository) ListAllLines(ctx context.Context, limit int, afterID string) ([]Line, error) {
	rows, err := r.pool.Query(ctx, lineSelect+` WHERE`+singleHopWhere+` AND l.id::text>$2 ORDER BY l.id::text LIMIT $1`, limit, afterID)
	if err != nil {
		return nil, fmt.Errorf("list all lines: %w", err)
	}
	return collectLines(rows)
}

func (r *PostgresRepository) ListAllowedLines(ctx context.Context, userID string, limit int, afterID string) ([]Line, error) {
	rows, err := r.pool.Query(ctx, lineSelect+allowedLineWhere+` AND l.id::text>$3 ORDER BY l.id::text LIMIT $2`, userID, limit, afterID)
	if err != nil {
		return nil, fmt.Errorf("list allowed lines: %w", err)
	}
	return collectLines(rows)
}

func (r *PostgresRepository) GetAllowedLine(ctx context.Context, userID, lineID string) (Line, error) {
	line, err := scanLine(r.pool.QueryRow(ctx, lineSelect+allowedLineWhere+` AND l.id=$2`, userID, lineID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Line{}, ErrNotFound
	}
	if err != nil {
		return Line{}, fmt.Errorf("get allowed line: %w", err)
	}
	return line, nil
}

func (r *PostgresRepository) GetLine(ctx context.Context, lineID string) (Line, error) {
	line, err := scanLine(r.pool.QueryRow(ctx, lineSelect+` WHERE`+singleHopWhere+` AND l.id=$1`, lineID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Line{}, ErrNotFound
	}
	if err != nil {
		return Line{}, fmt.Errorf("get line: %w", err)
	}
	return line, nil
}

func (r *PostgresRepository) GetUsableLine(ctx context.Context, userID, lineID string) (Line, error) {
	line, err := scanLine(r.pool.QueryRow(ctx, lineSelect+allowedLineWhere+`
AND l.enabled AND n.enabled AND g.enabled AND n.proxy_port IS NOT NULL
AND 'proxy'=ANY(n.capabilities) AND l.id=$2`, userID, lineID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Line{}, ErrNotFound
	}
	if err != nil {
		return Line{}, fmt.Errorf("get usable line: %w", err)
	}
	return line, nil
}
