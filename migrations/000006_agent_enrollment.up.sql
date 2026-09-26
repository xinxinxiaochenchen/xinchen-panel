CREATE TABLE agent_enrollment_tokens (
    id uuid PRIMARY KEY,
    node_id uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX agent_enrollment_one_unused_per_node
    ON agent_enrollment_tokens(node_id) WHERE consumed_at IS NULL;
CREATE INDEX agent_enrollment_expires_at_idx ON agent_enrollment_tokens(expires_at);

ALTER TABLE agents ADD COLUMN cert_expires_at timestamptz;
