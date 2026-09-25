CREATE TABLE roles (
    code text PRIMARY KEY,
    description text NOT NULL
);

CREATE UNIQUE INDEX users_email_lower_uniq ON users (lower(email));

CREATE TABLE permissions (
    code text PRIMARY KEY,
    description text NOT NULL
);

CREATE TABLE user_roles (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_code text NOT NULL REFERENCES roles(code),
    PRIMARY KEY (user_id, role_code)
);
CREATE INDEX user_roles_role_code_idx ON user_roles(role_code);

CREATE TABLE role_permissions (
    role_code text NOT NULL REFERENCES roles(code) ON DELETE CASCADE,
    permission_code text NOT NULL REFERENCES permissions(code) ON DELETE CASCADE,
    PRIMARY KEY (role_code, permission_code)
);

CREATE TABLE browser_sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    csrf_hash bytea NOT NULL CHECK (octet_length(csrf_hash) = 32),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > created_at)
);
CREATE INDEX browser_sessions_user_id_idx ON browser_sessions(user_id);
CREATE INDEX browser_sessions_expires_at_idx ON browser_sessions(expires_at);

INSERT INTO roles(code, description) VALUES
    ('admin', 'Administrator with global resource authority'),
    ('user', 'Member with plan-scoped resource authority');

INSERT INTO permissions(code, description) VALUES
    ('dashboard.read', 'View own dashboard'),
    ('nodes.read', 'View permitted nodes'),
    ('nodes.write', 'Manage global nodes'),
    ('lines.read', 'View permitted lines'),
    ('lines.write.self', 'Manage permitted own lines'),
    ('lines.write', 'Manage global lines'),
    ('forward_rules.read', 'View own forwarding rules'),
    ('forward_rules.write', 'Manage own forwarding rules'),
    ('subscriptions.read', 'View own subscriptions'),
    ('subscriptions.write', 'Manage own subscriptions'),
    ('routing.read', 'View own routing profiles'),
    ('routing.write', 'Manage own routing profiles'),
    ('usage.read', 'View own usage'),
    ('usage.admin', 'View all users usage'),
    ('plans.read', 'View plans'),
    ('plans.write', 'Manage plans'),
    ('users.read', 'View users'),
    ('users.write', 'Manage users'),
    ('agents.read', 'View agent state'),
    ('agents.write', 'Manage agent enrollment'),
    ('audit.read', 'View audit log');

INSERT INTO role_permissions(role_code, permission_code)
SELECT 'admin', code FROM permissions;

INSERT INTO role_permissions(role_code, permission_code)
SELECT 'user', code FROM permissions
WHERE code IN (
    'dashboard.read', 'nodes.read', 'lines.read', 'lines.write.self',
    'forward_rules.read', 'forward_rules.write',
    'subscriptions.read', 'subscriptions.write',
    'routing.read', 'routing.write', 'usage.read'
);
