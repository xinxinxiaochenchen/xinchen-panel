package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"

	"controlplane/internal/agentruntime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAgentNotFound    = errors.New("agent record not found")
	ErrRevisionNotFound = errors.New("configuration revision not found")
	ErrDigestMismatch   = errors.New("configuration digest mismatch")
	ErrResultConflict   = errors.New("configuration result conflict")
)

type DesiredRevision struct {
	NodeID       string
	Revision     int64
	Digest       string
	Snapshot     agentruntime.Snapshot
	Diagnostics  []ForwardRejection
	Status       string
	CreatedAt    time.Time
	AppliedAt    *time.Time
	ErrorCode    *string
	ErrorMessage *string
}

type RevisionRepository struct{ pool *pgxpool.Pool }

func NewRevisionRepository(pool *pgxpool.Pool) *RevisionRepository {
	return &RevisionRepository{pool: pool}
}

// Reconcile locks the Agent before reading source facts. Each attempt uses one
// database snapshot, and PostgreSQL serialization failures retry the complete
// read so an older worker cannot publish stale facts after a newer worker.
func (r *RevisionRepository) Reconcile(ctx context.Context, nodeID string) (DesiredRevision, bool, error) {
	for attempt := 0; attempt < 5; attempt++ {
		value, created, err := r.reconcileOnce(ctx, nodeID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "40001" {
			continue
		}
		return value, created, err
	}
	return DesiredRevision{}, false, errors.New("configuration reconcile exhausted serialization retries")
}

func (r *RevisionRepository) reconcileOnce(ctx context.Context, nodeID string) (DesiredRevision, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return DesiredRevision{}, false, fmt.Errorf("begin configuration reconcile: %w", err)
	}
	defer tx.Rollback(ctx)
	var current int64
	var capabilities []string
	if err := tx.QueryRow(ctx, `SELECT desired_revision,capabilities FROM agents WHERE node_id=$1 FOR UPDATE`, nodeID).Scan(&current, &capabilities); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DesiredRevision{}, false, ErrAgentNotFound
		}
		return DesiredRevision{}, false, fmt.Errorf("lock agent revision: %w", err)
	}
	node, err := readForwardNode(ctx, tx, nodeID)
	if err != nil {
		return DesiredRevision{}, false, err
	}
	var facts []ForwardFacts
	if node.Enabled && node.GroupEnabled && node.ForwardCapable {
		facts, err = readForwardFacts(ctx, tx, nodeID)
		if err != nil {
			return DesiredRevision{}, false, err
		}
	}
	compiled, err := CompileForwardSnapshot(node, facts, uint64(current)+1)
	if err != nil {
		return DesiredRevision{}, false, err
	}
	if node.Enabled && node.GroupEnabled && node.ProxyCapable && slices.Contains(capabilities, "proxy") {
		proxyFacts, err := readProxyFacts(ctx, tx, nodeID)
		if err != nil {
			return DesiredRevision{}, false, err
		}
		proxyConfig, err := CompileProxySnapshot(node, proxyFacts, uint64(current)+1)
		if err != nil {
			return DesiredRevision{}, false, err
		}
		compiled.Snapshot.ProxyConfig = proxyConfig.Snapshot.ProxyConfig
		compiled.Rejected = append(compiled.Rejected, proxyConfig.Rejected...)
	}
	payload, digest, err := CanonicalForwardPayload(compiled)
	if err != nil {
		return DesiredRevision{}, false, err
	}
	diagnostics, err := json.Marshal(compiled.Rejected)
	if err != nil {
		return DesiredRevision{}, false, fmt.Errorf("encode forward diagnostics: %w", err)
	}
	if len(compiled.Rejected) == 0 {
		diagnostics = []byte("[]")
	}
	if current > 0 {
		latest, err := readRevision(ctx, tx, nodeID, current)
		if err != nil {
			return DesiredRevision{}, false, err
		}
		if latest.Digest == digest {
			if latest.Status == "applied" || latest.Status == "rejected" {
				if err := recordProxyApplyStatus(ctx, tx, nodeID, current, payload, latest.Status, true); err != nil {
					return DesiredRevision{}, false, err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE config_revisions SET diagnostics_json=$3
WHERE node_id=$1 AND revision=$2 AND diagnostics_json IS DISTINCT FROM $3::jsonb`, nodeID, current, diagnostics); err != nil {
				return DesiredRevision{}, false, fmt.Errorf("refresh configuration diagnostics: %w", err)
			}
			latest, err = readRevision(ctx, tx, nodeID, current)
			if err != nil {
				return DesiredRevision{}, false, err
			}
			if err := tx.Commit(ctx); err != nil {
				return DesiredRevision{}, false, fmt.Errorf("commit unchanged configuration: %w", err)
			}
			return latest, false, nil
		}
	}
	if current == math.MaxInt64 {
		return DesiredRevision{}, false, errors.New("configuration revision exhausted")
	}
	next := current + 1
	if _, err := tx.Exec(ctx, `INSERT INTO config_revisions(node_id,revision,sha256,payload_json,diagnostics_json,created_at)
VALUES ($1,$2,$3,$4,$5,clock_timestamp())`, nodeID, next, digest, payload, diagnostics); err != nil {
		return DesiredRevision{}, false, fmt.Errorf("insert configuration revision: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET desired_revision=$2 WHERE node_id=$1`, nodeID, next); err != nil {
		return DesiredRevision{}, false, fmt.Errorf("advance agent desired revision: %w", err)
	}
	staged, err := readRevision(ctx, tx, nodeID, next)
	if err != nil {
		return DesiredRevision{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DesiredRevision{}, false, fmt.Errorf("commit configuration revision: %w", err)
	}
	return staged, true, nil
}

func (r *RevisionRepository) Desired(ctx context.Context, nodeID string) (DesiredRevision, error) {
	var desired int64
	if err := r.pool.QueryRow(ctx, `SELECT desired_revision FROM agents WHERE node_id=$1`, nodeID).Scan(&desired); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DesiredRevision{}, ErrAgentNotFound
		}
		return DesiredRevision{}, fmt.Errorf("read agent desired revision: %w", err)
	}
	if desired == 0 {
		return DesiredRevision{}, ErrRevisionNotFound
	}
	return readRevision(ctx, r.pool, nodeID, desired)
}

type revisionQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readRevision(ctx context.Context, querier revisionQuerier, nodeID string, revision int64) (DesiredRevision, error) {
	var value DesiredRevision
	var payload []byte
	var appliedAt *time.Time
	var diagnostics []byte
	err := querier.QueryRow(ctx, `SELECT node_id::text,revision,sha256,payload_json,diagnostics_json,status,created_at,applied_at,error_code,error_message
FROM config_revisions WHERE node_id=$1 AND revision=$2`, nodeID, revision).Scan(
		&value.NodeID, &value.Revision, &value.Digest, &payload, &diagnostics, &value.Status, &value.CreatedAt,
		&appliedAt, &value.ErrorCode, &value.ErrorMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return DesiredRevision{}, ErrRevisionNotFound
	}
	if err != nil {
		return DesiredRevision{}, fmt.Errorf("read configuration revision: %w", err)
	}
	value.AppliedAt = appliedAt
	var executable struct {
		ForwardConfig []agentruntime.Rule        `json:"forward_config"`
		ProxyConfig   []agentruntime.ProxyAccess `json:"proxy_config,omitempty"`
	}
	if err := json.Unmarshal(payload, &executable); err != nil {
		return DesiredRevision{}, fmt.Errorf("decode configuration revision: %w", err)
	}
	if err := json.Unmarshal(diagnostics, &value.Diagnostics); err != nil {
		return DesiredRevision{}, fmt.Errorf("decode configuration diagnostics: %w", err)
	}
	value.Snapshot = agentruntime.Snapshot{Revision: uint64(value.Revision), Rules: executable.ForwardConfig, ProxyConfig: executable.ProxyConfig}
	return value, nil
}

var resultCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// RecordResult accepts only a result for a stored node/revision/digest tuple.
// A repeated identical result is harmless; a late rejection cannot overwrite
// an applied result.
func (r *RevisionRepository) RecordResult(ctx context.Context, nodeID string, revision int64, digest, status, code, message string) error {
	if revision < 1 || (status != "applied" && status != "rejected") ||
		(status == "applied" && (code != "" || message != "")) ||
		(status == "rejected" && (!resultCodePattern.MatchString(code) || len(message) > 1024 || !utf8.ValidString(message) ||
			containsControl(message))) {
		return ErrResultConflict
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin configuration result: %w", err)
	}
	defer tx.Rollback(ctx)
	var desired, applied int64
	if err := tx.QueryRow(ctx, `SELECT desired_revision,applied_revision FROM agents WHERE node_id=$1 FOR UPDATE`, nodeID).Scan(&desired, &applied); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAgentNotFound
		}
		return fmt.Errorf("lock result agent: %w", err)
	}
	if revision > desired {
		return ErrRevisionNotFound
	}
	var storedDigest, priorStatus string
	var payload []byte
	var priorCode, priorMessage *string
	err = tx.QueryRow(ctx, `SELECT sha256,status,error_code,error_message,payload_json FROM config_revisions
WHERE node_id=$1 AND revision=$2 FOR UPDATE`, nodeID, revision).Scan(&storedDigest, &priorStatus, &priorCode, &priorMessage, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRevisionNotFound
	}
	if err != nil {
		return fmt.Errorf("lock configuration result: %w", err)
	}
	if storedDigest != digest {
		return ErrDigestMismatch
	}
	if revision < applied {
		if priorStatus == status && (status == "applied" ||
			(priorCode != nil && *priorCode == code && priorMessage != nil && *priorMessage == message)) {
			return tx.Commit(ctx)
		}
		return ErrResultConflict
	}
	if priorStatus == "applied" {
		if status != "applied" {
			return ErrResultConflict
		}
		// Re-enrollment resets the Agent's applied pointer while preserving
		// historical revisions. A fresh process ACKing the same digest restores
		// its current execution state without changing the immutable revision.
		if _, err := tx.Exec(ctx, `UPDATE agents SET applied_revision=GREATEST(applied_revision,$2) WHERE node_id=$1`, nodeID, revision); err != nil {
			return fmt.Errorf("restore agent applied revision: %w", err)
		}
		return tx.Commit(ctx)
	}
	if status == "applied" {
		if _, err := tx.Exec(ctx, `UPDATE config_revisions SET status='applied',applied_at=now(),error_code=NULL,error_message=NULL
WHERE node_id=$1 AND revision=$2`, nodeID, revision); err != nil {
			return fmt.Errorf("mark configuration applied: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE agents SET applied_revision=GREATEST(applied_revision,$2) WHERE node_id=$1`, nodeID, revision); err != nil {
			return fmt.Errorf("advance agent applied revision: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE config_revisions SET status='rejected',error_code=$3,error_message=$4
WHERE node_id=$1 AND revision=$2`, nodeID, revision, code, message); err != nil {
			return fmt.Errorf("mark configuration rejected: %w", err)
		}
	}
	if revision == desired {
		if err := recordProxyApplyStatus(ctx, tx, nodeID, revision, payload, status, false); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit configuration result: %w", err)
	}
	return nil
}

func containsControl(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}
