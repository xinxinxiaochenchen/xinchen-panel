CREATE TABLE agent_certificate_grants (
    node_id uuid NOT NULL REFERENCES agents(node_id) ON DELETE CASCADE,
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    parent_fingerprint text NOT NULL CHECK (parent_fingerprint ~ '^[0-9a-f]{64}$'),
    certificate_pem bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, fingerprint),
    UNIQUE (node_id, parent_fingerprint)
);
CREATE INDEX agent_certificate_grants_expiry_idx ON agent_certificate_grants(expires_at);
