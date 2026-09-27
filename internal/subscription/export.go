package subscription

import (
	"context"
	"controlplane/internal/catalog"
	"controlplane/internal/georules"
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
	if format != "clash" && format != "mihomo" && format != "sing-box" && format != "surge" {
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
	rows, err := tx.Query(ctx, `SELECT a.id::text,a.name,a.line_id::text,l.name,COALESCE(l.owner_user_id::text,''),
ARRAY(SELECT nh.group_id::text FROM line_hops lh JOIN nodes nh ON nh.id=lh.node_id WHERE lh.line_id=l.id ORDER BY lh.position),
n.region,COALESCE(host(n.public_ip),n.host),n.host,n.proxy_port,a.credential_ciphertext
FROM subscription_proxy_targets t JOIN proxy_accesses a ON a.id=t.proxy_access_id AND a.user_id=t.user_id
JOIN lines l ON l.id=a.line_id JOIN line_hops h ON h.line_id=l.id AND h.position=0 AND h.role IN ('egress','ingress')
JOIN nodes n ON n.id=h.node_id JOIN resource_groups g ON g.id=n.group_id
JOIN agents ag ON ag.node_id=n.id JOIN config_revisions cr ON cr.node_id=ag.node_id AND cr.revision=ag.applied_revision
WHERE t.subscription_id=$1 AND t.user_id=$2 AND a.enabled AND a.apply_status='active'
AND l.enabled AND n.enabled AND g.enabled AND 'proxy'=ANY(n.capabilities) AND n.proxy_port IS NOT NULL
AND ag.status='online' AND ag.last_seen_at>clock_timestamp()-interval '45 seconds'
AND cr.status='applied' AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(cr.payload_json->'proxy_config','[]'::jsonb)) p
WHERE p->>'id'=a.id::text AND p->>'credential_hash'=a.credential_hash)
AND (h.role='egress' OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(cr.payload_json->'relay_config','[]'::jsonb)) rc
WHERE rc->>'line_id'=l.id::text AND rc->>'generation'=l.relay_generation::text AND rc->>'role'='ingress'))
AND NOT EXISTS(SELECT 1 FROM line_hops h2 JOIN nodes n2 ON n2.id=h2.node_id
JOIN resource_groups g2 ON g2.id=n2.group_id
LEFT JOIN agents a2 ON a2.node_id=n2.id
LEFT JOIN config_revisions cr2 ON cr2.node_id=n2.id AND cr2.revision=a2.applied_revision
WHERE h2.line_id=l.id AND (NOT n2.enabled OR NOT g2.enabled OR
(h.role='ingress' AND (n2.relay_port IS NULL OR NOT 'forward'=ANY(n2.capabilities) OR
a2.status IS DISTINCT FROM 'online' OR a2.last_seen_at<=clock_timestamp()-interval '45 seconds' OR
a2.desired_revision IS DISTINCT FROM a2.applied_revision OR cr2.status IS DISTINCT FROM 'applied' OR
NOT EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(cr2.payload_json->'relay_config','[]'::jsonb)) rc2
WHERE rc2->>'line_id'=l.id::text AND rc2->>'generation'=l.relay_generation::text)))))
ORDER BY t.sort_order`, sub.ID, sub.UserID)
	if err != nil {
		return nil, "", fmt.Errorf("query subscription targets: %w", err)
	}
	targets := make([]subscriptionconfig.Target, 0)
	for rows.Next() {
		var v subscriptionconfig.Target
		var lineOwner, sealed string
		var groups []string
		if err := rows.Scan(&v.ID, &v.Name, &v.LineID, &v.LineName, &lineOwner, &groups, &v.Region, &v.Server, &v.ServerName, &v.Port, &sealed); err != nil {
			rows.Close()
			return nil, "", err
		}
		if !lineAllowedHops(sub.UserID, v.LineID, lineOwner, groups, grant) {
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
	policy, err := loadExportPolicy(ctx, tx, sub, targets)
	if err != nil {
		return nil, "", err
	}
	body, contentType, err := subscriptionconfig.RenderWithRouting(format, sub.NameTemplate, targets, policy)
	if err != nil {
		return nil, "", ValidationError{"routing_profile_id", err.Error()}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return body, contentType, nil
}

func loadExportPolicy(ctx context.Context, tx pgx.Tx, sub Subscription, targets []subscriptionconfig.Target) (*subscriptionconfig.RoutingPolicy, error) {
	if sub.RoutingProfileID == nil {
		return nil, nil
	}
	var kind string
	var line *string
	if err := tx.QueryRow(ctx, `SELECT fallback_kind,fallback_line_id::text FROM routing_profiles WHERE id=$1 AND user_id=$2 AND enabled`, *sub.RoutingProfileID, sub.UserID).Scan(&kind, &line); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnavailable
		}
		return nil, err
	}
	policy := &subscriptionconfig.RoutingPolicy{Fallback: subscriptionconfig.Action{Kind: kind}, RuleSets: map[string]subscriptionconfig.RuleSet{}}
	if line != nil {
		policy.Fallback.LineID = *line
	}
	rows, err := tx.Query(ctx, `SELECT match_type,match_value,action,line_id::text FROM routing_rules WHERE profile_id=$1 AND enabled ORDER BY priority,id`, *sub.RoutingProfileID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rule subscriptionconfig.Rule
		var line *string
		if err := rows.Scan(&rule.MatchType, &rule.MatchValue, &rule.Action.Kind, &line); err != nil {
			rows.Close()
			return nil, err
		}
		if line != nil {
			rule.Action.LineID = *line
		}
		policy.Rules = append(policy.Rules, rule)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, rule := range policy.Rules {
		if rule.MatchType != "geosite" && rule.MatchType != "geoip" {
			continue
		}
		set, err := georules.LoadEnabled(ctx, tx, rule.MatchType, rule.MatchValue)
		if err == nil {
			policy.RuleSets[rule.MatchType+":"+rule.MatchValue] = subscriptionconfig.RuleSet{Kind: set.Kind, Code: set.Code, Version: set.Version, SHA256: set.SHA256, Entries: set.Entries}
			continue
		}
		if !errors.Is(err, georules.ErrNotFound) {
			return nil, err
		}
		if rule.MatchType == "geosite" {
			return nil, ErrUnavailable
		}
	}
	available := map[string]bool{}
	for _, target := range targets {
		available[target.LineID] = true
	}
	needed := map[string]bool{}
	if policy.Fallback.Kind == "line" {
		needed[policy.Fallback.LineID] = true
	}
	for _, rule := range policy.Rules {
		if rule.Action.Kind == "line" {
			needed[rule.Action.LineID] = true
		}
	}
	for id := range needed {
		if !available[id] {
			return nil, ErrUnavailable
		}
	}
	return policy, nil
}
