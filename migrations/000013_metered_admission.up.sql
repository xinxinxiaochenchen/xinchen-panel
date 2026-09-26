-- Resource attribution is frozen when the control plane admits a connection.
-- Historical sessions deliberately keep the resource UUID without a foreign key:
-- deleting a rule must not invalidate late usage replay.
ALTER TABLE usage_sessions
    ADD COLUMN resource_kind text,
    ADD COLUMN resource_id uuid,
    ADD COLUMN config_revision bigint,
    ADD CONSTRAINT usage_sessions_resource_binding CHECK (
      (resource_kind IS NULL AND resource_id IS NULL AND config_revision IS NULL)
      OR (resource_kind IS NOT NULL AND resource_kind IN ('forward','proxy') AND resource_id IS NOT NULL AND config_revision IS NOT NULL AND config_revision > 0)
    );
CREATE INDEX usage_sessions_resource_idx ON usage_sessions(resource_kind,resource_id,started_at DESC)
WHERE resource_kind IS NOT NULL;

-- A lease belongs to exactly one admitted connection, including before its
-- first usage event. Keep the association after a resource is deleted so
-- delayed reports and settlements remain attributable.
CREATE TABLE connection_lease_bindings (
    connection_id uuid NOT NULL REFERENCES usage_sessions(id),
    lease_id uuid NOT NULL UNIQUE REFERENCES quota_leases(id),
    config_revision bigint NOT NULL CHECK (config_revision > 0),
    PRIMARY KEY (connection_id,lease_id)
);
