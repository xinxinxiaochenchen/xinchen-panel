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
	} `json:"limits"`
}

func (r *PostgresRepository) Create(ctx context.Context, ownerID string, input AccessInput, requestID string) (Access, string, error) {
	input, err := NormalizeAccess(NewAccess{Name: input.Name, LineID: input.LineID, Enabled: &input.Enabled})
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
	membershipID, err := authorizeProxyLine(ctx, tx, ownerID, input.LineID)
	if err != nil {
		return Access{}, "", err
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
	var groupID string
	var lineEnabled, nodeEnabled, groupEnabled bool
	var proxyPort *int
	var capabilities []string
	err = tx.QueryRow(ctx, `SELECT l.owner_user_id::text,n.group_id::text,l.enabled,n.enabled,g.enabled,n.proxy_port,n.capabilities
FROM lines l JOIN line_hops h ON h.line_id=l.id AND h.position=0 AND h.role='egress'
JOIN nodes n ON n.id=h.node_id JOIN resource_groups g ON g.id=n.group_id
WHERE l.id=$1 AND NOT EXISTS (SELECT 1 FROM line_hops extra WHERE extra.line_id=l.id AND extra.position<>0)
FOR SHARE OF l,n,g`, lineID).Scan(&lineOwner, &groupID, &lineEnabled, &nodeEnabled, &groupEnabled, &proxyPort, &capabilities)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock proxy access line: %w", err)
	}
	if !lineEnabled || !nodeEnabled || !groupEnabled || proxyPort == nil || !slices.Contains(capabilities, "proxy") ||
		!slices.Contains(entitlement.ResourceGroupIDs, groupID) {
		return "", ErrNotFound
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

const accessSelect = `SELECT id::text,user_id::text,line_id::text,name,enabled,apply_status,created_at,updated_at FROM proxy_accesses`

func scanAccess(row pgx.Row) (Access, error) {
	var value Access
	err := row.Scan(&value.ID, &value.UserID, &value.LineID, &value.Name, &value.Enabled,
		&value.ApplyStatus, &value.CreatedAt, &value.UpdatedAt)
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
