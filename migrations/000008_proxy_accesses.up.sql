INSERT INTO permissions(code, description) VALUES
    ('proxy_accesses.read', 'View own proxy accesses and credential'),
    ('proxy_accesses.write', 'Manage own proxy accesses');
INSERT INTO role_permissions(role_code, permission_code)
SELECT roles.code, permissions.code FROM roles CROSS JOIN permissions
WHERE roles.code IN ('admin', 'user') AND permissions.code IN ('proxy_accesses.read', 'proxy_accesses.write');

CREATE TABLE proxy_accesses (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    line_id uuid NOT NULL REFERENCES lines(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    credential_hash char(56) NOT NULL UNIQUE CHECK (credential_hash ~ '^[0-9a-f]{56}$'),
    credential_ciphertext text NOT NULL CHECK (char_length(credential_ciphertext) BETWEEN 32 AND 512),
    enabled boolean NOT NULL DEFAULT true,
    apply_status text NOT NULL DEFAULT 'pending' CHECK (apply_status IN ('pending', 'active', 'apply_failed', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT proxy_accesses_owner_name_unique UNIQUE (user_id, name)
);
CREATE INDEX proxy_accesses_user_id_idx ON proxy_accesses(user_id, id);
CREATE INDEX proxy_accesses_line_id_idx ON proxy_accesses(line_id);
