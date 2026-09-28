-- Restore the previous schema and system permissions only.
-- Policy data, subscription bindings, custom role grants and former limits
-- must be recovered from the database backup made before migration 28.
ALTER TABLE plan_limits ADD COLUMN max_routing_rules integer NOT NULL DEFAULT 0
    CHECK (max_routing_rules >= 0);

INSERT INTO permissions(code,description) VALUES
    ('routing.read','View own routing profiles'),
    ('routing.write','Manage own routing profiles');
INSERT INTO role_permissions(role_code,permission_code)
SELECT roles.code,permissions.code FROM roles CROSS JOIN permissions
WHERE roles.code IN ('admin','user') AND permissions.code IN ('routing.read','routing.write');

CREATE TABLE routing_profiles (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    fallback_kind text NOT NULL CHECK (fallback_kind IN ('direct','block','line')),
    fallback_line_id uuid REFERENCES lines(id) ON DELETE RESTRICT,
    enabled boolean NOT NULL DEFAULT true,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id,name),
    UNIQUE (id,user_id),
    CHECK ((fallback_kind='line') = (fallback_line_id IS NOT NULL))
);
CREATE INDEX routing_profiles_user_id_idx ON routing_profiles(user_id,id);

CREATE TABLE routing_rules (
    id uuid PRIMARY KEY,
    profile_id uuid NOT NULL REFERENCES routing_profiles(id) ON DELETE CASCADE,
    priority integer NOT NULL CHECK (priority BETWEEN 1 AND 1000000),
    match_type text NOT NULL CHECK (match_type IN ('domain','domain_suffix','ip','cidr','geoip','geosite')),
    match_value text NOT NULL CHECK (char_length(match_value) BETWEEN 1 AND 253),
    action text NOT NULL CHECK (action IN ('direct','block','line')),
    line_id uuid REFERENCES lines(id) ON DELETE RESTRICT,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (profile_id,priority),
    CHECK ((action='line') = (line_id IS NOT NULL))
);
CREATE INDEX routing_rules_profile_priority_idx ON routing_rules(profile_id,priority);

ALTER TABLE subscriptions ADD COLUMN routing_profile_id uuid;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_routing_profile_owner_fk
    FOREIGN KEY (routing_profile_id,user_id) REFERENCES routing_profiles(id,user_id) ON DELETE RESTRICT;
CREATE INDEX subscriptions_routing_profile_idx ON subscriptions(routing_profile_id) WHERE routing_profile_id IS NOT NULL;

INSERT INTO permissions(code, description) VALUES
    ('routing_rulesets.read', 'View managed GeoSite and GeoIP rule-set metadata'),
    ('routing_rulesets.write', 'Upload and activate managed GeoSite and GeoIP rule-sets');
INSERT INTO role_permissions(role_code, permission_code)
SELECT 'admin', code FROM permissions WHERE code IN ('routing_rulesets.read', 'routing_rulesets.write');

CREATE TABLE routing_rule_sets (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('geosite','geoip')),
    code text NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9_-]{1,63}$'),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    version text NOT NULL CHECK (char_length(version) BETWEEN 1 AND 64),
    source text NOT NULL CHECK (char_length(source) BETWEEN 1 AND 500),
    sha256 char(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    entries jsonb NOT NULL CHECK (jsonb_typeof(entries) = 'array'),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, code, version)
);
CREATE UNIQUE INDEX routing_rule_sets_active_idx ON routing_rule_sets(kind, code) WHERE enabled;
