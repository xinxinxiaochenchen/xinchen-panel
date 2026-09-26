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
