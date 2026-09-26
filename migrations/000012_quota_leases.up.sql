ALTER TABLE memberships ADD COLUMN initial_snapshot_json jsonb;
UPDATE memberships SET initial_snapshot_json=snapshot_json;
ALTER TABLE billing_periods ADD COLUMN activated_at timestamptz;
ALTER TABLE billing_periods ADD COLUMN reserved_bytes bigint NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0);

CREATE TABLE quota_leases (
    id uuid PRIMARY KEY,
    billing_period_id uuid NOT NULL REFERENCES billing_periods(id),
    agent_id uuid NOT NULL REFERENCES agents(id),
    request_id uuid NOT NULL,
    requested_bytes bigint NOT NULL CHECK (requested_bytes > 0 AND requested_bytes <= 1048576),
    granted_bytes bigint NOT NULL CHECK (granted_bytes > 0 AND granted_bytes <= requested_bytes),
    consumed_bytes bigint NOT NULL DEFAULT 0 CHECK (consumed_bytes >= 0),
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    state text NOT NULL DEFAULT 'active' CHECK (state IN ('active','settled')),
    settled_at timestamptz,
    CHECK (expires_at > issued_at),
    CHECK ((state='active' AND settled_at IS NULL) OR (state='settled' AND settled_at IS NOT NULL)),
    UNIQUE(agent_id,request_id),
    UNIQUE(id,billing_period_id,agent_id)
);
CREATE INDEX quota_leases_period_state_idx ON quota_leases(billing_period_id,state);
CREATE INDEX quota_leases_agent_state_idx ON quota_leases(agent_id,state);

-- Existing ledger rows remain readable; the repository requires a lease for all new writes.
ALTER TABLE usage_sessions ADD COLUMN first_lease_id uuid REFERENCES quota_leases(id);
ALTER TABLE usage_events ADD COLUMN lease_id uuid REFERENCES quota_leases(id);
CREATE INDEX usage_events_lease_idx ON usage_events(lease_id);
