CREATE EXTENSION IF NOT EXISTS btree_gist;

ALTER TABLE memberships ADD COLUMN period_months integer NOT NULL DEFAULT 1 CHECK (period_months BETWEEN 1 AND 120);
UPDATE memberships m SET period_months=p.period_months FROM plans p WHERE p.id=m.plan_id;

CREATE TABLE billing_periods (
    id uuid PRIMARY KEY,
    membership_id uuid NOT NULL REFERENCES memberships(id),
    user_id uuid NOT NULL REFERENCES users(id),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    quota_bytes bigint NOT NULL CHECK (quota_bytes >= 0),
    uploaded_bytes bigint NOT NULL DEFAULT 0 CHECK (uploaded_bytes >= 0),
    downloaded_bytes bigint NOT NULL DEFAULT 0 CHECK (downloaded_bytes >= 0),
    charged_bytes bigint NOT NULL DEFAULT 0 CHECK (charged_bytes >= 0),
    snapshot_json jsonb NOT NULL,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at),
    UNIQUE (membership_id, starts_at),
    UNIQUE (id, user_id),
    EXCLUDE USING gist (membership_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
);
CREATE INDEX billing_periods_user_starts_idx ON billing_periods(user_id, starts_at DESC);

-- One row per first-ingress connection. All attribution and multiplier data are
-- frozen here; the Agent may report byte counters but cannot choose a rate.
CREATE TABLE usage_sessions (
    id uuid PRIMARY KEY,
    billing_period_id uuid NOT NULL,
    user_id uuid NOT NULL,
    agent_id uuid NOT NULL REFERENCES agents(id),
    ingress_node_id uuid NOT NULL REFERENCES nodes(id),
    line_id uuid REFERENCES lines(id),
    multiplier_milli integer NOT NULL CHECK (multiplier_milli BETWEEN 1 AND 100000),
    started_at timestamptz NOT NULL,
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    uploaded_bytes bigint NOT NULL DEFAULT 0 CHECK (uploaded_bytes >= 0),
    downloaded_bytes bigint NOT NULL DEFAULT 0 CHECK (downloaded_bytes >= 0),
    charged_bytes bigint NOT NULL DEFAULT 0 CHECK (charged_bytes >= 0),
    last_observed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (billing_period_id, user_id) REFERENCES billing_periods(id, user_id)
);
CREATE INDEX usage_sessions_agent_period_idx ON usage_sessions(agent_id, billing_period_id);

CREATE TABLE usage_events (
    id uuid PRIMARY KEY,
    connection_id uuid NOT NULL REFERENCES usage_sessions(id),
    billing_period_id uuid NOT NULL REFERENCES billing_periods(id),
    user_id uuid NOT NULL REFERENCES users(id),
    ingress_node_id uuid NOT NULL REFERENCES nodes(id),
    line_id uuid REFERENCES lines(id),
    sequence bigint NOT NULL CHECK (sequence > 0),
    cumulative_uploaded_bytes bigint NOT NULL CHECK (cumulative_uploaded_bytes >= 0),
    cumulative_downloaded_bytes bigint NOT NULL CHECK (cumulative_downloaded_bytes >= 0),
    uploaded_bytes bigint NOT NULL CHECK (uploaded_bytes >= 0),
    downloaded_bytes bigint NOT NULL CHECK (downloaded_bytes >= 0),
    multiplier_milli integer NOT NULL CHECK (multiplier_milli BETWEEN 1 AND 100000),
    charged_bytes bigint NOT NULL CHECK (charged_bytes >= 0),
    observed_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (connection_id, sequence)
);
CREATE INDEX usage_events_user_observed_idx ON usage_events(user_id, observed_at DESC);
CREATE INDEX usage_events_node_observed_idx ON usage_events(ingress_node_id, observed_at DESC);
CREATE INDEX usage_events_line_observed_idx ON usage_events(line_id, observed_at DESC) WHERE line_id IS NOT NULL;
CREATE INDEX usage_events_period_observed_idx ON usage_events(billing_period_id, observed_at DESC);

CREATE FUNCTION reject_usage_event_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'usage_events is append only';
END;
$$;
CREATE TRIGGER usage_events_immutable BEFORE UPDATE OR DELETE ON usage_events
FOR EACH ROW EXECUTE FUNCTION reject_usage_event_change();
