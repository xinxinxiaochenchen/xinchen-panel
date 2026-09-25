CREATE TABLE users (
    id uuid PRIMARY KEY,
    email text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'disabled')),
    timezone text NOT NULL DEFAULT 'Asia/Shanghai',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE resource_groups (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    region text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE nodes (
    id uuid PRIMARY KEY,
    group_id uuid NOT NULL REFERENCES resource_groups(id),
    name text NOT NULL,
    region text NOT NULL,
    host text NOT NULL,
    public_ip inet,
    proxy_port integer CHECK (proxy_port BETWEEN 1 AND 65535),
    capabilities text[] NOT NULL CHECK (
        cardinality(capabilities) > 0 AND capabilities <@ ARRAY['proxy', 'forward']::text[]
    ),
    bandwidth_bps bigint CHECK (bandwidth_bps >= 0),
    multiplier_milli integer CHECK (multiplier_milli > 0),
    tags text[] NOT NULL DEFAULT '{}',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX nodes_group_id_idx ON nodes(group_id);

CREATE TABLE agents (
    id uuid PRIMARY KEY,
    node_id uuid NOT NULL UNIQUE REFERENCES nodes(id),
    cert_fingerprint text UNIQUE,
    version text,
    desired_revision bigint NOT NULL DEFAULT 0 CHECK (desired_revision >= 0),
    applied_revision bigint NOT NULL DEFAULT 0 CHECK (applied_revision >= 0),
    last_seen_at timestamptz,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'online', 'offline', 'revoked')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE lines (
    id uuid PRIMARY KEY,
    owner_user_id uuid REFERENCES users(id),
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    priority integer NOT NULL DEFAULT 100,
    weight integer NOT NULL DEFAULT 1 CHECK (weight > 0),
    multiplier_milli integer CHECK (multiplier_milli > 0),
    tags text[] NOT NULL DEFAULT '{}',
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX lines_shared_name_uniq ON lines(name) WHERE owner_user_id IS NULL;
CREATE UNIQUE INDEX lines_owner_name_uniq ON lines(owner_user_id, name) WHERE owner_user_id IS NOT NULL;

CREATE TABLE line_hops (
    line_id uuid NOT NULL REFERENCES lines(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    node_id uuid NOT NULL REFERENCES nodes(id),
    role text NOT NULL CHECK (role IN ('ingress', 'relay', 'egress')),
    PRIMARY KEY (line_id, position),
    UNIQUE (line_id, node_id)
);
CREATE INDEX line_hops_node_id_idx ON line_hops(node_id);

CREATE TABLE plans (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    billing_mode text NOT NULL DEFAULT 'monthly_anchor' CHECK (billing_mode = 'monthly_anchor'),
    period_months integer NOT NULL DEFAULT 1 CHECK (period_months > 0),
    quota_bytes bigint NOT NULL CHECK (quota_bytes >= 0),
    default_multiplier_milli integer NOT NULL DEFAULT 1000 CHECK (default_multiplier_milli > 0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE plan_limits (
    plan_id uuid PRIMARY KEY REFERENCES plans(id) ON DELETE CASCADE,
    max_forward_rules_per_node integer NOT NULL DEFAULT 0 CHECK (max_forward_rules_per_node >= 0),
    max_subscriptions integer NOT NULL DEFAULT 0 CHECK (max_subscriptions >= 0),
    max_routing_rules integer NOT NULL DEFAULT 0 CHECK (max_routing_rules >= 0),
    allow_custom_lines boolean NOT NULL DEFAULT false,
    max_custom_lines integer NOT NULL DEFAULT 0 CHECK (max_custom_lines >= 0),
    max_hops integer NOT NULL DEFAULT 1 CHECK (max_hops BETWEEN 1 AND 8)
);

CREATE TABLE plan_resource_group_grants (
    plan_id uuid NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    resource_group_id uuid NOT NULL REFERENCES resource_groups(id),
    allowed boolean NOT NULL DEFAULT true,
    PRIMARY KEY (plan_id, resource_group_id)
);

CREATE TABLE plan_line_grants (
    plan_id uuid NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    line_id uuid NOT NULL REFERENCES lines(id),
    allowed boolean NOT NULL DEFAULT true,
    PRIMARY KEY (plan_id, line_id)
);

CREATE TABLE memberships (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    plan_id uuid NOT NULL REFERENCES plans(id),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    status text NOT NULL CHECK (status IN ('scheduled', 'active', 'expired', 'cancelled')),
    anchor_day integer NOT NULL CHECK (anchor_day BETWEEN 1 AND 31),
    timezone text NOT NULL,
    snapshot_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at)
);
CREATE UNIQUE INDEX memberships_one_active_per_user ON memberships(user_id) WHERE status = 'active';
CREATE INDEX memberships_user_starts_at_idx ON memberships(user_id, starts_at DESC);

CREATE TABLE outbox_events (
    id uuid PRIMARY KEY,
    kind text NOT NULL,
    aggregate_id uuid NOT NULL,
    payload jsonb NOT NULL,
    idempotency_key text NOT NULL UNIQUE,
    retry_count integer NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_events_pending_idx ON outbox_events(available_at) WHERE processed_at IS NULL;
