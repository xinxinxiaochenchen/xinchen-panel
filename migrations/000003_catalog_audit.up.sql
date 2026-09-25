CREATE TABLE audit_logs (
    id uuid PRIMARY KEY,
    actor_user_id uuid NOT NULL REFERENCES users(id),
    action text NOT NULL,
    object_type text NOT NULL,
    object_id uuid NOT NULL,
    before_json jsonb,
    after_json jsonb,
    request_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_actor_created_idx ON audit_logs(actor_user_id, created_at DESC);
CREATE INDEX audit_logs_object_created_idx ON audit_logs(object_type, object_id, created_at DESC);

CREATE UNIQUE INDEX nodes_proxy_endpoint_uniq ON nodes (lower(host), proxy_port)
WHERE proxy_port IS NOT NULL;
