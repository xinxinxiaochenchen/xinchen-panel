CREATE TABLE config_revisions (
    node_id uuid NOT NULL REFERENCES agents(node_id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision > 0),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    payload_json jsonb NOT NULL CHECK (jsonb_typeof(payload_json) = 'object'),
    diagnostics_json jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(diagnostics_json) = 'array'),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'applied', 'rejected')),
    error_code text,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    applied_at timestamptz,
    PRIMARY KEY (node_id, revision),
    CHECK (status <> 'applied' OR applied_at IS NOT NULL)
);
