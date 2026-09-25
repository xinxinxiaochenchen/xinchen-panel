INSERT INTO permissions(code, description) VALUES
    ('forward_policies.write', 'Manage approved forwarding target types and ports');
INSERT INTO role_permissions(role_code, permission_code) VALUES
    ('admin', 'forward_policies.write');

CREATE TABLE forward_target_policies (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('public_host', 'node')),
    target_group_id uuid REFERENCES resource_groups(id),
    protocol text NOT NULL CHECK (protocol IN ('TCP', 'UDP')),
    port_start integer NOT NULL CHECK (port_start BETWEEN 1 AND 65535),
    port_end integer NOT NULL CHECK (port_end BETWEEN port_start AND 65535),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT forward_target_policies_target CHECK ((kind = 'node') = (target_group_id IS NOT NULL))
);
CREATE UNIQUE INDEX forward_target_policies_public_unique ON forward_target_policies(protocol, port_start, port_end)
WHERE kind = 'public_host';
CREATE UNIQUE INDEX forward_target_policies_node_unique ON forward_target_policies(target_group_id, protocol, port_start, port_end)
WHERE kind = 'node';
CREATE INDEX forward_target_policies_match_idx ON forward_target_policies(kind, protocol, port_start, port_end)
WHERE enabled;

CREATE TABLE forward_rules (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    ingress_node_id uuid NOT NULL REFERENCES nodes(id),
    ingress_port integer NOT NULL CHECK (ingress_port BETWEEN 1024 AND 65535),
    target_node_id uuid REFERENCES nodes(id),
    target_host text,
    target_port integer NOT NULL CHECK (target_port BETWEEN 1 AND 65535),
    line_id uuid REFERENCES lines(id),
    protocol text NOT NULL CHECK (protocol IN ('TCP', 'UDP', 'BOTH')),
    enabled boolean NOT NULL DEFAULT true,
    apply_status text NOT NULL DEFAULT 'pending' CHECK (apply_status IN ('pending', 'active', 'apply_failed', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT forward_rules_one_target CHECK ((target_node_id IS NULL) <> (target_host IS NULL)),
    CONSTRAINT forward_rules_direct_only CHECK (line_id IS NULL),
    CONSTRAINT forward_rules_user_name_unique UNIQUE (user_id, name)
);
CREATE INDEX forward_rules_ingress_node_idx ON forward_rules(ingress_node_id);
CREATE INDEX forward_rules_user_id_idx ON forward_rules(user_id, id);

CREATE TABLE port_allocations (
    id uuid PRIMARY KEY,
    node_id uuid NOT NULL REFERENCES nodes(id),
    protocol text NOT NULL CHECK (protocol IN ('TCP', 'UDP')),
    port integer NOT NULL CHECK (port BETWEEN 1024 AND 65535),
    owner_type text NOT NULL CHECK (owner_type = 'forward_rule'),
    owner_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    released_at timestamptz
);
CREATE UNIQUE INDEX port_allocations_active_unique ON port_allocations(node_id, protocol, port)
WHERE released_at IS NULL;
CREATE INDEX port_allocations_owner_idx ON port_allocations(owner_type, owner_id);
