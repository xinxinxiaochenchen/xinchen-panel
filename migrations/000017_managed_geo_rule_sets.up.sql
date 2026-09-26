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
