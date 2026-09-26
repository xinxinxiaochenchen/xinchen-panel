package subscription

import (
	"context"
	"controlplane/internal/catalog"
	"controlplane/internal/subscriptionconfig"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
)

// Export rechecks every target against the current frozen membership and the
// Agent's last ACKed executable credential. A pending rotation is never shown.
func (r *PostgresRepository) Export(ctx context.Context, token, format string) ([]byte, string, error) {
	hash, err := TokenHash(token)
	if err != nil {
		return nil, "", ErrNotFound
	}
	return r.export(ctx, "", "", hash, format)
}
func (r *PostgresRepository) ExportOwn(ctx context.Context, owner, subID, format string) ([]byte, string, error) {
	if !catalog.ValidID(owner) || !catalog.ValidID(subID) {
		return nil, "", ErrNotFound
	}
	return r.export(ctx, owner, subID, "", format)
}
func (r *PostgresRepository) export(ctx context.Context, owner, subID, hash, format string) ([]byte, string, error) {
	if format != "mihomo" && format != "sing-box" {
		return nil, "", ValidationError{"format", "unsupported format"}
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)
	var sub Subscription
	if hash != "" {
		sub, err = scanSubscription(tx.QueryRow(ctx, subscriptionSelect+` WHERE s.token_hash=$1 AND s.enabled`, hash))
	} else {
		sub, err = scanSubscription(tx.QueryRow(ctx, subscriptionSelect+` WHERE s.id=$1 AND s.user_id=$2 AND s.enabled`, subID, owner))
	}
	if err != nil {
		return nil, "", err
	}
	grant, ends, err := loadGrant(ctx, tx, sub.UserID)
	if errors.Is(err, ErrNotFound) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	rows, err := tx.Query(ctx, `SELECT a.id::text,a.name,a.line_id::text,l.name,COALESCE(l.owner_user_id::text,''),n.group_id::text,
n.region,COALESCE(host(n.public_ip),n.host),n.host,n.proxy_port,a.credential_ciphertext
FROM subscription_proxy_targets t JOIN proxy_accesses a ON a.id=t.proxy_access_id AND a.user_id=t.user_id
JOIN lines l ON l.id=a.line_id JOIN line_hops h ON h.line_id=l.id AND h.position=0 AND h.role='egress'
JOIN nodes n ON n.id=h.node_id JOIN resource_groups g ON g.id=n.group_id
JOIN agents ag ON ag.node_id=n.id JOIN config_revisions cr ON cr.node_id=ag.node_id AND cr.revision=ag.applied_revision
WHERE t.subscription_id=$1 AND t.user_id=$2 AND a.enabled AND a.apply_status='active'
AND l.enabled AND n.enabled AND g.enabled AND 'proxy'=ANY(n.capabilities) AND n.proxy_port IS NOT NULL
AND ag.status='online' AND ag.last_seen_at>clock_timestamp()-interval '45 seconds'
AND cr.status='applied' AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(cr.payload_json->'proxy_config','[]'::jsonb)) p
WHERE p->>'id'=a.id::text AND p->>'credential_hash'=a.credential_hash)
AND NOT EXISTS(SELECT 1 FROM line_hops h2 WHERE h2.line_id=l.id AND h2.position<>0)
ORDER BY t.sort_order`, sub.ID, sub.UserID)
	if err != nil {
		return nil, "", fmt.Errorf("query subscription targets: %w", err)
	}
	targets := make([]subscriptionconfig.Target, 0)
	for rows.Next() {
		var v subscriptionconfig.Target
		var lineID, lineOwner, group, sealed string
		if err := rows.Scan(&v.ID, &v.Name, &lineID, &v.LineName, &lineOwner, &group, &v.Region, &v.Server, &v.ServerName, &v.Port, &sealed); err != nil {
			rows.Close()
			return nil, "", err
		}
		if !lineAllowed(sub.UserID, lineID, lineOwner, group, grant) {
			continue
		}
		password, err := r.cipher.Open(v.ID, sub.UserID, sealed)
		if err != nil {
			rows.Close()
			return nil, "", fmt.Errorf("decrypt subscription target: %w", err)
		}
		v.Password = password
		targets = append(targets, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, "", err
	}
	rows.Close()
	if len(targets) == 0 {
		return nil, "", ErrUnavailable
	}
	if err := checkExpiry(ctx, tx, ends); err != nil {
		return nil, "", ErrNotFound
	}
	body, contentType, err := subscriptionconfig.Render(format, sub.NameTemplate, targets)
	if err != nil {
		return nil, "", fmt.Errorf("render subscription: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return body, contentType, nil
}
