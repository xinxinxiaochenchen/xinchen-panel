package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
)

// OpenRequest identifies a resource in the Agent's applied executable revision.
// User, period, multiplier and lease lifetime are deliberately absent.
type OpenRequest struct {
	ConnectionID   string
	RequestID      string
	ResourceKind   string
	ResourceID     string
	LineID         string
	Revision       int64
	RequestedBytes int64
}

type RenewRequest struct {
	ConnectionID   string
	RequestID      string
	Revision       int64
	RequestedBytes int64
}

type AdmissionGrant struct {
	ConnectionID    string
	MultiplierMilli int64
	PeriodEndsAt    time.Time
	Lease           Lease
}

func validateOpenRequest(req OpenRequest) error {
	if !uuidPattern.MatchString(req.ConnectionID) || !uuidPattern.MatchString(req.RequestID) || !uuidPattern.MatchString(req.ResourceID) ||
		(req.LineID != "" && !uuidPattern.MatchString(req.LineID)) ||
		(req.ResourceKind != "forward" && req.ResourceKind != "proxy") || req.Revision < 1 || req.RequestedBytes < 1 || req.RequestedBytes > MaxLeaseBytes {
		return ErrNotFound
	}
	if req.ResourceKind != "proxy" && req.LineID != "" {
		return ErrNotFound
	}
	return nil
}

// OpenConnection atomically freezes attribution and reserves the first small
// quota lease. The caller must use the certificate-authenticated node ID.
func (r *PostgresRepository) OpenConnection(ctx context.Context, nodeID string, req OpenRequest) (AdmissionGrant, error) {
	if !uuidPattern.MatchString(nodeID) {
		return AdmissionGrant{}, ErrNotFound
	}
	if err := validateOpenRequest(req); err != nil {
		return AdmissionGrant{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AdmissionGrant{}, err
	}
	defer tx.Rollback(ctx)
	agentID, err := agentForNode(ctx, tx, nodeID)
	if err != nil {
		return AdmissionGrant{}, err
	}
	if grant, found, err := existingAdmission(ctx, tx, agentID, req); err != nil {
		return AdmissionGrant{}, err
	} else if found {
		return grant, tx.Commit(ctx)
	}
	ownerID, lineID, err := resourceOwner(ctx, tx, req.ResourceKind, req.ResourceID)
	if err != nil {
		return AdmissionGrant{}, err
	}
	_, period, now, err := lockCurrentPeriod(ctx, tx, ownerID)
	if err != nil {
		return AdmissionGrant{}, err
	}
	if grant, found, err := existingAdmission(ctx, tx, agentID, req); err != nil {
		return AdmissionGrant{}, err
	} else if found {
		return grant, tx.Commit(ctx)
	}
	if req.ResourceKind == "proxy" && req.LineID != "" {
		lineID = req.LineID
	}
	if err := requestIDAvailable(ctx, tx, agentID, req.RequestID); err != nil {
		return AdmissionGrant{}, err
	}
	multiplier, err := authorizeResource(ctx, tx, nodeID, agentID, period.ID, req, lineID, now)
	if err != nil {
		return AdmissionGrant{}, err
	}
	lease, err := reserveAdmissionLease(ctx, tx, agentID, period, req.RequestID, req.RequestedBytes, now)
	if err != nil {
		return AdmissionGrant{}, err
	}
	var lineArg any
	if lineID != "" {
		lineArg = lineID
	}
	tag, err := tx.Exec(ctx, `INSERT INTO usage_sessions(id,billing_period_id,user_id,agent_id,ingress_node_id,line_id,multiplier_milli,started_at,first_lease_id,resource_kind,resource_id,config_revision)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (id) DO NOTHING`, req.ConnectionID, period.ID, ownerID, agentID, nodeID, lineArg, multiplier, lease.IssuedAt, lease.ID, req.ResourceKind, req.ResourceID, req.Revision)
	if err != nil {
		return AdmissionGrant{}, databaseError(err)
	}
	if tag.RowsAffected() != 1 {
		return AdmissionGrant{}, ErrConflict
	}
	if err := bindConnectionLease(ctx, tx, req.ConnectionID, lease.ID, req.Revision); err != nil {
		return AdmissionGrant{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_periods SET reserved_bytes=reserved_bytes+$2 WHERE id=$1`, period.ID, lease.GrantedBytes); err != nil {
		return AdmissionGrant{}, databaseError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AdmissionGrant{}, databaseError(err)
	}
	return AdmissionGrant{ConnectionID: req.ConnectionID, MultiplierMilli: multiplier, PeriodEndsAt: period.EndsAt, Lease: lease}, nil
}

func agentForNode(ctx context.Context, tx pgx.Tx, nodeID string) (string, error) {
	var agentID string
	err := tx.QueryRow(ctx, `SELECT id::text FROM agents WHERE node_id=$1`, nodeID).Scan(&agentID)
	return agentID, databaseError(err)
}

func existingAdmission(ctx context.Context, tx pgx.Tx, agentID string, req OpenRequest) (AdmissionGrant, bool, error) {
	var grant AdmissionGrant
	var kind, resourceID, frozenLine string
	var revision int64
	var leaseID string
	err := tx.QueryRow(ctx, `SELECT s.resource_kind,s.resource_id::text,s.config_revision,s.multiplier_milli,p.ends_at,s.first_lease_id::text,COALESCE(s.line_id::text,'')
FROM usage_sessions s JOIN billing_periods p ON p.id=s.billing_period_id
WHERE s.id=$1 AND s.agent_id=$2`, req.ConnectionID, agentID).Scan(&kind, &resourceID, &revision, &grant.MultiplierMilli, &grant.PeriodEndsAt, &leaseID, &frozenLine)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdmissionGrant{}, false, nil
	}
	if err != nil {
		return AdmissionGrant{}, false, databaseError(err)
	}
	if kind != req.ResourceKind || !sameID(resourceID, req.ResourceID) || revision != req.Revision ||
		(req.ResourceKind == "proxy" && req.LineID != "" && !sameID(frozenLine, req.LineID)) {
		return AdmissionGrant{}, false, ErrConflict
	}
	lease, err := scanLease(tx.QueryRow(ctx, `SELECT `+leaseColumns+` FROM quota_leases WHERE id=$1 AND agent_id=$2`, leaseID, agentID))
	if err != nil {
		return AdmissionGrant{}, false, err
	}
	if !sameID(lease.RequestID, req.RequestID) || lease.RequestedBytes != req.RequestedBytes {
		return AdmissionGrant{}, false, ErrConflict
	}
	grant.ConnectionID = req.ConnectionID
	grant.Lease = lease
	return grant, true, nil
}

func requestIDAvailable(ctx context.Context, tx pgx.Tx, agentID, requestID string) error {
	var found string
	err := tx.QueryRow(ctx, `SELECT id::text FROM quota_leases WHERE agent_id=$1 AND request_id=$2`, agentID, requestID).Scan(&found)
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return databaseError(err)
	}
	return nil
}

func resourceOwner(ctx context.Context, tx pgx.Tx, kind, resourceID string) (string, string, error) {
	var owner, lineID string
	switch kind {
	case "forward":
		err := tx.QueryRow(ctx, `SELECT user_id::text,COALESCE(line_id::text,'') FROM forward_rules WHERE id=$1`, resourceID).Scan(&owner, &lineID)
		return owner, lineID, databaseError(err)
	case "proxy":
		err := tx.QueryRow(ctx, `SELECT user_id::text,line_id::text FROM proxy_accesses WHERE id=$1`, resourceID).Scan(&owner, &lineID)
		return owner, lineID, databaseError(err)
	default:
		return "", "", ErrNotFound
	}
}

func lockCurrentPeriod(ctx context.Context, tx pgx.Tx, ownerID string) (string, Period, time.Time, error) {
	var locked string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE id=$1 AND status='active' FOR SHARE`, ownerID).Scan(&locked); err != nil {
		return "", Period{}, time.Time{}, databaseError(err)
	}
	var memberID string
	var memberStart, memberEnd time.Time
	err := tx.QueryRow(ctx, `SELECT id::text,starts_at,ends_at FROM memberships WHERE user_id=$1 AND status='active' FOR SHARE`, ownerID).Scan(&memberID, &memberStart, &memberEnd)
	if err != nil {
		return "", Period{}, time.Time{}, databaseError(err)
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return "", Period{}, time.Time{}, err
	}
	if now.Before(memberStart) || !now.Before(memberEnd) {
		return "", Period{}, time.Time{}, ErrNotFound
	}
	period, err := scanPeriod(tx.QueryRow(ctx, `SELECT `+periodColumns+` FROM billing_periods WHERE membership_id=$1 AND status='open' AND activated_at IS NOT NULL AND starts_at<=$2 AND ends_at>$2 FOR UPDATE`, memberID, now))
	if err != nil {
		return "", Period{}, time.Time{}, err
	}
	// Refresh the clock after waiting on the period lock. A request that straddles
	// the boundary must not receive a lease in the old period.
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return "", Period{}, time.Time{}, err
	}
	if !now.Before(period.EndsAt) || !now.Before(memberEnd) {
		return "", Period{}, time.Time{}, ErrNotFound
	}
	return memberID, period, now, nil
}

func authorizeResource(ctx context.Context, tx pgx.Tx, nodeID, agentID, periodID string, req OpenRequest, lineID string, now time.Time) (int64, error) {
	var lastSeen time.Time
	var applied int64
	var agentStatus string
	err := tx.QueryRow(ctx, `SELECT a.last_seen_at,a.applied_revision,a.status
FROM agents a JOIN nodes n ON n.id=a.node_id JOIN resource_groups g ON g.id=n.group_id
WHERE a.id=$1 AND n.id=$2 AND n.enabled AND g.enabled FOR SHARE OF a,n,g`, agentID, nodeID).Scan(&lastSeen, &applied, &agentStatus)
	if err != nil {
		return 0, databaseError(err)
	}
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return 0, err
	}
	if agentStatus != "online" || now.Sub(lastSeen) > 45*time.Second || applied != req.Revision {
		return 0, ErrNotFound
	}
	var payload []byte
	err = tx.QueryRow(ctx, `SELECT payload_json FROM config_revisions WHERE node_id=$1 AND revision=$2 AND status='applied'`, nodeID, req.Revision).Scan(&payload)
	if err != nil {
		return 0, databaseError(err)
	}
	var authorized bool
	var multiplier int64
	if req.ResourceKind == "forward" {
		err = tx.QueryRow(ctx, `SELECT COALESCE(l.multiplier_milli,n.multiplier_milli,(p.snapshot_json->>'default_multiplier_milli')::bigint),
 EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE($4::jsonb->'forward_config','[]'::jsonb)) e
 WHERE e->>'id'=f.id::text AND (e->>'ingress_port')::int=f.ingress_port
 AND e->>'target_host'=COALESCE(f.target_host,host(nt.public_ip),nt.host,'')
 AND (e->>'target_port')::int=f.target_port AND e->>'protocol'=f.protocol AND e->>'enabled'='true'
 AND (f.line_id IS NULL AND COALESCE(e->>'line_id','')=''
      OR f.line_id IS NOT NULL AND e->>'line_id'=f.line_id::text
         AND (e->>'relay_generation')::bigint=l.relay_generation))
 AND COALESCE(p.snapshot_json->'resource_group_ids','[]'::jsonb) ? n.group_id::text
 AND COALESCE((p.snapshot_json->'limits'->>'max_forward_rules_per_node')::int,0)>0
 AND (f.target_node_id IS NULL OR (nt.enabled AND gt.enabled AND COALESCE(p.snapshot_json->'resource_group_ids','[]'::jsonb) ? nt.group_id::text))
 AND NOT EXISTS(SELECT 1 FROM unnest(CASE WHEN f.protocol='BOTH' THEN ARRAY['TCP','UDP']::text[] ELSE ARRAY[f.protocol] END) proto
 WHERE NOT EXISTS(SELECT 1 FROM forward_target_policies fp WHERE fp.enabled
 AND fp.kind=CASE WHEN f.target_node_id IS NULL THEN 'public_host' ELSE 'node' END
 AND fp.target_group_id IS NOT DISTINCT FROM nt.group_id AND fp.protocol=proto
 AND fp.port_start<=f.target_port AND fp.port_end>=f.target_port))
 FROM forward_rules f JOIN nodes n ON n.id=f.ingress_node_id
 JOIN billing_periods p ON p.id=$3 LEFT JOIN nodes nt ON nt.id=f.target_node_id
 LEFT JOIN lines l ON l.id=f.line_id
 LEFT JOIN resource_groups gt ON gt.id=nt.group_id
 WHERE f.id=$1 AND f.ingress_node_id=$2 AND f.user_id=p.user_id AND f.enabled
 AND (f.line_id IS NULL OR (l.enabled AND EXISTS(SELECT 1 FROM line_hops lh WHERE lh.line_id=l.id AND lh.position=0 AND lh.node_id=n.id)
   AND ((l.owner_user_id IS NULL AND COALESCE(p.snapshot_json->'line_ids','[]'::jsonb) ? l.id::text)
     OR (l.owner_user_id=f.user_id AND COALESCE((p.snapshot_json->'limits'->>'allow_custom_lines')::boolean,false)))
   )) FOR SHARE OF f`, req.ResourceID, nodeID, periodID, payload).Scan(&multiplier, &authorized)
	} else {
		err = tx.QueryRow(ctx, `SELECT COALESCE(l.multiplier_milli,n.multiplier_milli,(p.snapshot_json->>'default_multiplier_milli')::bigint),
EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE($4::jsonb->'proxy_config','[]'::jsonb)) e
WHERE e->>'id'=a.id::text AND e->>'user_id'=a.user_id::text
AND e->>'credential_hash'=a.credential_hash AND (e->>'ingress_port')::int=n.proxy_port
AND (e->>'expires_at')::timestamptz>=$5
AND (COALESCE(jsonb_array_length(e->'candidates'),0)=0
     AND e->>'line_id'=l.id::text AND (topology.hop_count=1 AND COALESCE((e->>'relay_generation')::bigint,0)=0
     OR topology.hop_count>1 AND (e->>'relay_generation')::bigint=l.relay_generation)
     OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(e->'candidates','[]'::jsonb)) candidate
       WHERE candidate->>'line_id'=l.id::text
       AND (topology.hop_count=1 AND COALESCE((candidate->>'relay_generation')::bigint,0)=0
         OR topology.hop_count>1 AND (candidate->>'relay_generation')::bigint=l.relay_generation))))
AND COALESCE(p.snapshot_json->'resource_group_ids','[]'::jsonb) ? n.group_id::text
AND (l.owner_user_id IS NULL AND COALESCE(p.snapshot_json->'line_ids','[]'::jsonb) ? l.id::text
     OR l.owner_user_id=a.user_id AND COALESCE((p.snapshot_json->'limits'->>'allow_custom_lines')::boolean,false))
AND topology.hop_count BETWEEN 1 AND 8
AND topology.hop_count<=COALESCE((p.snapshot_json->'limits'->>'max_hops')::int,1)
AND (topology.hop_count=1 AND h.role='egress' OR topology.hop_count>1 AND h.role='ingress')
AND NOT EXISTS(SELECT 1 FROM line_hops step
JOIN nodes hop_node ON hop_node.id=step.node_id
JOIN resource_groups hop_group ON hop_group.id=hop_node.group_id
LEFT JOIN agents hop_agent ON hop_agent.node_id=hop_node.id
WHERE step.line_id=l.id AND (
    step.position>=topology.hop_count
    OR step.role<>CASE WHEN step.position=topology.hop_count-1 THEN 'egress'
                       WHEN step.position=0 THEN 'ingress' ELSE 'relay' END
    OR NOT hop_node.enabled OR NOT hop_group.enabled
    OR NOT (COALESCE(p.snapshot_json->'resource_group_ids','[]'::jsonb) ? hop_node.group_id::text)
    OR topology.hop_count=1 AND (hop_node.proxy_port IS NULL OR NOT 'proxy'=ANY(hop_node.capabilities))
    OR topology.hop_count>1 AND (
         hop_node.relay_port IS NULL OR NOT 'forward'=ANY(hop_node.capabilities)
         OR step.position=0 AND (hop_node.proxy_port IS NULL OR NOT 'proxy'=ANY(hop_node.capabilities))
         OR hop_agent.status IS DISTINCT FROM 'online'
         OR hop_agent.last_seen_at<=clock_timestamp()-interval '45 seconds'
         OR hop_agent.desired_revision IS DISTINCT FROM hop_agent.applied_revision
         OR NOT 'relay'=ANY(hop_agent.capabilities)
         OR NOT (COALESCE(hop_agent.cert_expires_at>clock_timestamp(),false)
                 OR EXISTS(SELECT 1 FROM agent_certificate_grants cg
                           WHERE cg.node_id=hop_node.id AND cg.expires_at>clock_timestamp()))
         OR NOT EXISTS(SELECT 1 FROM agent_relay_certificate_grants rg
                       WHERE rg.node_id=hop_node.id AND rg.expires_at>clock_timestamp())
         OR NOT EXISTS(SELECT 1 FROM config_revisions hop_revision
                       WHERE hop_revision.node_id=hop_node.id AND hop_revision.revision=hop_agent.applied_revision
                         AND hop_revision.status='applied'
                         AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(hop_revision.payload_json->'relay_config','[]'::jsonb)) route
                                    WHERE route->>'line_id'=l.id::text
                                      AND route->>'generation'=l.relay_generation::text
                                      AND route->>'role'=step.role)))))
FROM proxy_accesses a JOIN lines l ON l.id=$6::uuid
LEFT JOIN proxy_access_lines pal ON pal.proxy_access_id=a.id AND pal.line_id=l.id
JOIN line_hops h ON h.line_id=l.id AND h.position=0
JOIN nodes n ON n.id=h.node_id
JOIN agents ag ON ag.node_id=n.id
JOIN LATERAL (SELECT count(*) AS hop_count FROM line_hops lh WHERE lh.line_id=l.id) topology ON true
JOIN billing_periods p ON p.id=$3
WHERE a.id=$1 AND h.node_id=$2 AND a.user_id=p.user_id
AND (a.line_id=l.id OR pal.line_id IS NOT NULL)
AND a.enabled AND l.enabled AND ag.status='online' AND ag.last_seen_at>clock_timestamp()-interval '45 seconds'
AND ag.applied_revision=$7 AND 'proxy'=ANY(ag.capabilities) FOR SHARE OF a,l,ag`, req.ResourceID, nodeID, periodID, payload, now, lineID, req.Revision).Scan(&multiplier, &authorized)
	}
	if err != nil {
		return 0, databaseError(err)
	}
	if authorized && req.ResourceKind == "forward" && lineID != "" {
		authorized, err = authorizeForwardRoute(ctx, tx, lineID, periodID, nodeID)
		if err != nil {
			return 0, err
		}
	}
	if !authorized || multiplier < 1 || multiplier > 100000 {
		return 0, ErrNotFound
	}
	return multiplier, nil
}

func reserveAdmissionLease(ctx context.Context, tx pgx.Tx, agentID string, period Period, requestID string, amount int64, now time.Time) (Lease, error) {
	var charged, reserved, quota int64
	var memberEnd time.Time
	err := tx.QueryRow(ctx, `SELECT p.quota_bytes,p.charged_bytes,p.reserved_bytes,m.ends_at FROM billing_periods p JOIN memberships m ON m.id=p.membership_id WHERE p.id=$1`, period.ID).Scan(&quota, &charged, &reserved, &memberEnd)
	if err != nil {
		return Lease{}, databaseError(err)
	}
	// Resource locks may have blocked since lockCurrentPeriod read the clock.
	// Never issue a lease using that stale time across a billing boundary.
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return Lease{}, err
	}
	if !now.Before(period.EndsAt) || !now.Before(memberEnd) {
		return Lease{}, ErrNotFound
	}
	if charged >= quota || reserved >= quota-charged {
		return Lease{}, ErrQuotaExhausted
	}
	grant := min(amount, quota-charged-reserved)
	expires := earlier(now.Add(LeaseLifetime), earlier(period.EndsAt, memberEnd))
	if !expires.After(now) {
		return Lease{}, ErrNotFound
	}
	leaseID, err := id.NewV7()
	if err != nil {
		return Lease{}, err
	}
	lease, err := scanLease(tx.QueryRow(ctx, `INSERT INTO quota_leases(id,agent_id,billing_period_id,request_id,requested_bytes,granted_bytes,issued_at,expires_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+leaseColumns, leaseID, agentID, period.ID, requestID, amount, grant, now, expires))
	if err != nil {
		return Lease{}, fmt.Errorf("reserve admission lease: %w", err)
	}
	return lease, nil
}

func (r *PostgresRepository) RenewConnectionLease(ctx context.Context, nodeID string, req RenewRequest) (AdmissionGrant, error) {
	if !uuidPattern.MatchString(nodeID) || !uuidPattern.MatchString(req.ConnectionID) || !uuidPattern.MatchString(req.RequestID) || req.Revision < 1 || req.RequestedBytes < 1 || req.RequestedBytes > MaxLeaseBytes {
		return AdmissionGrant{}, ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return AdmissionGrant{}, err
	}
	defer tx.Rollback(ctx)
	agentID, err := agentForNode(ctx, tx, nodeID)
	if err != nil {
		return AdmissionGrant{}, err
	}
	var ownerID, kind, resourceID, periodID string
	var frozenLine *string
	var multiplier int64
	err = tx.QueryRow(ctx, `SELECT user_id::text,resource_kind,resource_id::text,billing_period_id::text,line_id::text,multiplier_milli FROM usage_sessions WHERE id=$1 AND agent_id=$2 AND resource_kind IS NOT NULL`, req.ConnectionID, agentID).Scan(&ownerID, &kind, &resourceID, &periodID, &frozenLine, &multiplier)
	if err != nil {
		return AdmissionGrant{}, databaseError(err)
	}
	if kind == "" {
		return AdmissionGrant{}, ErrNotFound
	}
	// A retry of an already issued lease does not depend on mutable resource state.
	if prior, found, err := existingRenewal(ctx, tx, agentID, periodID, multiplier, req); err != nil {
		return AdmissionGrant{}, err
	} else if found {
		return prior, tx.Commit(ctx)
	}
	_, period, now, err := lockCurrentPeriod(ctx, tx, ownerID)
	if err != nil {
		return AdmissionGrant{}, err
	}
	if period.ID != periodID {
		return AdmissionGrant{}, ErrLeaseClosed
	}
	// Concurrent identical requests may have committed while this transaction
	// waited for the period lock. Return that grant without reserving again.
	if prior, found, err := existingRenewal(ctx, tx, agentID, periodID, multiplier, req); err != nil {
		return AdmissionGrant{}, err
	} else if found {
		return prior, tx.Commit(ctx)
	}
	currentOwner, currentLine, err := resourceOwner(ctx, tx, kind, resourceID)
	if err != nil {
		return AdmissionGrant{}, err
	}
	if !sameID(currentOwner, ownerID) {
		return AdmissionGrant{}, ErrNotFound
	}
	if kind == "proxy" && frozenLine == nil {
		return AdmissionGrant{}, ErrNotFound
	}
	if kind == "proxy" {
		currentLine = *frozenLine
	}
	if (frozenLine == nil && currentLine != "") || (frozenLine != nil && !sameID(currentLine, *frozenLine)) {
		return AdmissionGrant{}, ErrNotFound
	}
	freshMultiplier, err := authorizeResource(ctx, tx, nodeID, agentID, periodID, OpenRequest{ResourceKind: kind, ResourceID: resourceID, Revision: req.Revision}, currentLine, now)
	if err != nil {
		return AdmissionGrant{}, err
	}
	if freshMultiplier != multiplier {
		return AdmissionGrant{}, ErrNotFound
	}
	if err := requestIDAvailable(ctx, tx, agentID, req.RequestID); err != nil {
		return AdmissionGrant{}, err
	}
	lease, err := reserveAdmissionLease(ctx, tx, agentID, period, req.RequestID, req.RequestedBytes, now)
	if err != nil {
		return AdmissionGrant{}, err
	}
	if err := bindConnectionLease(ctx, tx, req.ConnectionID, lease.ID, req.Revision); err != nil {
		return AdmissionGrant{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_periods SET reserved_bytes=reserved_bytes+$2 WHERE id=$1`, periodID, lease.GrantedBytes); err != nil {
		return AdmissionGrant{}, databaseError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AdmissionGrant{}, databaseError(err)
	}
	return AdmissionGrant{ConnectionID: req.ConnectionID, MultiplierMilli: multiplier, PeriodEndsAt: period.EndsAt, Lease: lease}, nil
}

func (r *PostgresRepository) SettleConnectionLease(ctx context.Context, nodeID, connectionID, leaseID string, expected int64) (Lease, error) {
	if !uuidPattern.MatchString(nodeID) || !uuidPattern.MatchString(connectionID) || !uuidPattern.MatchString(leaseID) || expected < 0 {
		return Lease{}, ErrNotFound
	}
	agentID, err := r.AgentIDForNode(ctx, nodeID)
	if err != nil {
		return Lease{}, err
	}
	var belongs bool
	err = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM usage_sessions s
 JOIN connection_lease_bindings b ON b.connection_id=s.id
 JOIN quota_leases l ON l.id=b.lease_id AND l.billing_period_id=s.billing_period_id AND l.agent_id=s.agent_id
 WHERE s.id=$1 AND s.agent_id=$2 AND l.id=$3)`, connectionID, agentID, leaseID).Scan(&belongs)
	if err != nil {
		return Lease{}, err
	}
	if !belongs {
		return Lease{}, ErrNotFound
	}
	return r.SettleLease(ctx, agentID, leaseID, expected)
}

func bindConnectionLease(ctx context.Context, tx pgx.Tx, connectionID, leaseID string, revision int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO connection_lease_bindings(connection_id,lease_id,config_revision) VALUES($1,$2,$3)`, connectionID, leaseID, revision)
	return databaseError(err)
}

func existingRenewal(ctx context.Context, tx pgx.Tx, agentID, periodID string, multiplier int64, req RenewRequest) (AdmissionGrant, bool, error) {
	prior, err := scanLease(tx.QueryRow(ctx, `SELECT `+leaseColumns+` FROM quota_leases WHERE agent_id=$1 AND request_id=$2`, agentID, req.RequestID))
	if errors.Is(err, ErrNotFound) {
		return AdmissionGrant{}, false, nil
	}
	if err != nil {
		return AdmissionGrant{}, false, err
	}
	if !sameID(prior.PeriodID, periodID) || prior.RequestedBytes != req.RequestedBytes {
		return AdmissionGrant{}, false, ErrConflict
	}
	var ends time.Time
	err = tx.QueryRow(ctx, `SELECT p.ends_at FROM billing_periods p
 JOIN usage_sessions s ON s.billing_period_id=p.id
 JOIN connection_lease_bindings b ON b.connection_id=s.id
 WHERE s.id=$1 AND b.lease_id=$2 AND b.config_revision=$3 AND s.first_lease_id<>b.lease_id`, req.ConnectionID, prior.ID, req.Revision).Scan(&ends)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdmissionGrant{}, false, ErrConflict
	}
	if err != nil {
		return AdmissionGrant{}, false, databaseError(err)
	}
	return AdmissionGrant{ConnectionID: req.ConnectionID, MultiplierMilli: multiplier, PeriodEndsAt: ends, Lease: prior}, true, nil
}
