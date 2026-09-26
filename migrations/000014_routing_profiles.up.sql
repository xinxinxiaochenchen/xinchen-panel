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
