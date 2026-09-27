package proxyaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool   *pgxpool.Pool
	cipher *CredentialCipher
}

func NewPostgresRepository(pool *pgxpool.Pool, cipher *CredentialCipher) *PostgresRepository {
	return &PostgresRepository{pool: pool, cipher: cipher}
}

type accessEntitlement struct {
	ResourceGroupIDs []string `json:"resource_group_ids"`
	LineIDs          []string `json:"line_ids"`
	Limits           struct {
		AllowCustomLines bool `json:"allow_custom_lines"`
		MaxHops          int  `json:"max_hops"`
		MaxProxyLines    int  `json:"max_proxy_lines"`
	} `json:"limits"`
}

func (r *PostgresRepository) Create(ctx context.Context, ownerID string, input AccessInput, requestID string) (Access, string, error) {
	input, err := NormalizeAccess(NewAccess{Name: input.Name, LineID: input.LineID, LineIDs: input.LineIDs, LineOptions: input.LineOptions, Enabled: &input.Enabled})
	if err != nil {
		return Access{}, "", err
	}
	if !uuidPattern.MatchString(ownerID) || r.cipher == nil {
		return Access{}, "", ErrNotFound
	}
	accessID, err := id.NewV7()
	if err != nil {
		return Access{}, "", err
	}
	credential, err := GenerateCredential()
	if err != nil {
		return Access{}, "", err
	}
	sealed, err := r.cipher.Seal(accessID, ownerID, credential)
	if err != nil {
		return Access{}, "", err
	}
	digest := TrojanDigest(credential)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Access{}, "", fmt.Errorf("begin proxy access creation: %w", err)
	}
	defer tx.Rollback(ctx)
	var maxProxyLines int
	if err := tx.QueryRow(ctx, `SELECT COALESCE((snapshot_json->'limits'->>'max_proxy_lines')::int,1)
FROM memberships WHERE user_id=$1 AND status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp() FOR SHARE`, ownerID).Scan(&maxProxyLines); err != nil {
		return Access{}, "", proxyDatabaseError(err)
	}
	if len(input.LineIDs) > maxProxyLines {
		return Access{}, "", ValidationError{"line_ids", "exceeds plan proxy line limit"}
	}
	var membershipID string
	for index, lineID := range input.LineIDs {
		currentMembership, err := authorizeProxyLine(ctx, tx, ownerID, lineID)
		if err != nil {
			return Access{}, "", err
		}
		if index == 0 {
			membershipID = currentMembership
		} else if currentMembership != membershipID {
			return Access{}, "", ErrConflict
		}
	}
	applyStatus := "pending"
	if !input.Enabled {
		applyStatus = "disabled"
	}
	access := Access{ID: accessID, UserID: ownerID, LineID: input.LineID, Name: input.Name,
		Enabled: input.Enabled, ApplyStatus: applyStatus}
	err = tx.QueryRow(ctx, `INSERT INTO proxy_accesses(id,user_id,line_id,name,credential_hash,credential_ciphertext,enabled,apply_status)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at,updated_at`, access.ID, access.UserID,
		access.LineID, access.Name, digest, sealed, access.Enabled, access.ApplyStatus).Scan(&access.CreatedAt, &access.UpdatedAt)
	if err != nil {
		return Access{}, "", fmt.Errorf("insert proxy access: %w", proxyDatabaseError(err))
	}
	for index, option := range input.LineOptions {
		if _, err := tx.Exec(ctx, `INSERT INTO proxy_access_lines(proxy_access_id,line_id,position,priority,weight)
VALUES($1,$2,$3,$4,$5)`, access.ID, option.LineID, index, option.Priority, option.Weight); err != nil {
			return Access{}, "", fmt.Errorf("insert proxy access line: %w", proxyDatabaseError(err))
		}
	}
	access.LineIDs = append([]string(nil), input.LineIDs...)
	access.LineOptions = append([]LineOption(nil), input.LineOptions...)
	if err := recordAccessChange(ctx, tx, access, ownerID, "create", requestID); err != nil {
		return Access{}, "", err
	}
	var stillActive bool
	if err := tx.QueryRow(ctx, `SELECT status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp()
FROM memberships WHERE id=$1`, membershipID).Scan(&stillActive); err != nil {
		return Access{}, "", fmt.Errorf("recheck proxy access membership: %w", err)
	}
	if !stillActive {
		return Access{}, "", ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return Access{}, "", fmt.Errorf("commit proxy access: %w", proxyDatabaseError(err))
	}
	return access, credential, nil
}

func authorizeProxyLine(ctx context.Context, tx pgx.Tx, ownerID, lineID string) (string, error) {
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, ownerID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", fmt.Errorf("lock proxy access owner: %w", err)
	}
	if status != "active" {
		return "", ErrNotFound
	}
	var membershipID string
	var snapshotJSON []byte
	err := tx.QueryRow(ctx, `SELECT id::text,snapshot_json FROM memberships
WHERE user_id=$1 AND status='active' AND starts_at<=clock_timestamp() AND ends_at>clock_timestamp() FOR SHARE`, ownerID).Scan(&membershipID, &snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load proxy access entitlement: %w", err)
	}
	var entitlement accessEntitlement
	if err := json.Unmarshal(snapshotJSON, &entitlement); err != nil {
		return "", fmt.Errorf("decode proxy access entitlement: %w", err)
	}
	var lineOwner *string
	var lineEnabled bool
	err = tx.QueryRow(ctx, `SELECT owner_user_id::text,enabled FROM lines WHERE id=$1 FOR SHARE`, lineID).Scan(&lineOwner, &lineEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock proxy access line: %w", err)
	}
	if !lineEnabled {
		return "", ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT h.position,h.role,n.group_id::text,n.enabled,g.enabled,n.proxy_port,n.relay_port,n.capabilities
FROM line_hops h JOIN nodes n ON n.id=h.node_id JOIN resource_groups g ON g.id=n.group_id
WHERE h.line_id=$1 ORDER BY h.position FOR SHARE OF n,g`, lineID)
	if err != nil {
		return "", fmt.Errorf("lock proxy line hops: %w", err)
	}
	type hop struct {
		position                  int
		role, group               string
		nodeEnabled, groupEnabled bool
		proxyPort, relayPort      *int
		capabilities              []string
	}
	hops := make([]hop, 0, 8)
	for rows.Next() {
		var h hop
		if err := rows.Scan(&h.position, &h.role, &h.group, &h.nodeEnabled, &h.groupEnabled, &h.proxyPort, &h.relayPort, &h.capabilities); err != nil {
			rows.Close()
			return "", fmt.Errorf("scan proxy line hop: %w", err)
		}
		hops = append(hops, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", fmt.Errorf("read proxy line hops: %w", err)
	}
	if len(hops) == 0 || len(hops) > 8 || (len(hops) > 1 && entitlement.Limits.MaxHops < len(hops)) {
		return "", ErrNotFound
	}
	for position, h := range hops {
		expectedRole := "relay"
		if position == 0 && len(hops) > 1 {
			expectedRole = "ingress"
		}
		if position == len(hops)-1 {
			expectedRole = "egress"
		}
		if h.position != position || h.role != expectedRole || !h.nodeEnabled || !h.groupEnabled || !slices.Contains(entitlement.ResourceGroupIDs, h.group) {
			return "", ErrNotFound
		}
		if len(hops) == 1 {
			if h.proxyPort == nil || !slices.Contains(h.capabilities, "proxy") {
				return "", ErrNotFound
			}
		} else if h.relayPort == nil || !slices.Contains(h.capabilities, "forward") || position == 0 && (h.proxyPort == nil || !slices.Contains(h.capabilities, "proxy")) {
			return "", ErrNotFound
		}
	}
	if lineOwner == nil {
		if !slices.Contains(entitlement.LineIDs, lineID) {
			return "", ErrUnauthorized
		}
	} else if *lineOwner != ownerID || !entitlement.Limits.AllowCustomLines {
		return "", ErrUnauthorized
	}
	return membershipID, nil
}

const accessSelect = `SELECT a.id::text,a.user_id::text,a.line_id::text,a.name,a.enabled,a.apply_status,a.created_at,a.updated_at,
ARRAY(SELECT pal.line_id::text FROM proxy_access_lines pal WHERE pal.proxy_access_id=a.id ORDER BY pal.position),
COALESCE((SELECT jsonb_agg(jsonb_build_object('line_id',pal.line_id::text,'priority',pal.priority,'weight',pal.weight) ORDER BY pal.position)::text
FROM proxy_access_lines pal WHERE pal.proxy_access_id=a.id),'[]') FROM proxy_accesses a`

func scanAccess(row pgx.Row) (Access, error) {
	var value Access
	var optionsJSON []byte
	err := row.Scan(&value.ID, &value.UserID, &value.LineID, &value.Name, &value.Enabled,
		&value.ApplyStatus, &value.CreatedAt, &value.UpdatedAt, &value.LineIDs, &optionsJSON)
	if err == nil {
		if unmarshalErr := json.Unmarshal(optionsJSON, &value.LineOptions); unmarshalErr != nil {
			return Access{}, fmt.Errorf("decode proxy access line options: %w", unmarshalErr)
		}
	}
	return value, err
}

func (r *PostgresRepository) GetOwn(ctx context.Context, ownerID, accessID string) (Access, error) {
	value, err := scanAccess(r.pool.QueryRow(ctx, accessSelect+` WHERE id=$1 AND user_id=$2`, accessID, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Access{}, ErrNotFound
	}
	if err != nil {
		return Access{}, fmt.Errorf("get proxy access: %w", err)
	}
	return value, nil
}

func (r *PostgresRepository) ListOwn(ctx context.Context, ownerID string, limit int, afterID string) ([]Access, error) {
	rows, err := r.pool.Query(ctx, accessSelect+` WHERE user_id=$1 AND id::text>$3 ORDER BY id::text LIMIT $2`, ownerID, limit, afterID)
	if err != nil {
		return nil, fmt.Errorf("list proxy accesses: %w", err)
	}
	defer rows.Close()
	values := make([]Access, 0)
	for rows.Next() {
		value, err := scanAccess(rows)
		if err != nil {
			return nil, fmt.Errorf("scan proxy access: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxy accesses: %w", err)
	}
	return values, nil
}

func proxyDatabaseError(err error) error {
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
