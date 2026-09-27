CREATE TABLE agent_relay_certificate_grants (
    node_id uuid NOT NULL REFERENCES agents(node_id) ON DELETE CASCADE,
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    parent_fingerprint text CHECK (parent_fingerprint ~ '^[0-9a-f]{64}$'),
    csr_digest text NOT NULL CHECK (csr_digest ~ '^[0-9a-f]{64}$'),
    certificate_pem bytea NOT NULL,
    host text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (node_id, fingerprint),
    UNIQUE (node_id, parent_fingerprint)
);
CREATE INDEX agent_relay_certificate_grants_expiry_idx ON agent_relay_certificate_grants(expires_at);

-- Invalidate grants at the same database boundary as administrative changes.
-- Re-enabling a resource must never resurrect a previously revoked identity.
CREATE FUNCTION invalidate_agent_relay_certificates() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'agents' THEN
        IF NEW.status = 'revoked' OR NEW.cert_fingerprint IS DISTINCT FROM OLD.cert_fingerprint THEN
            DELETE FROM agent_relay_certificate_grants WHERE node_id = NEW.node_id;
        END IF;
    ELSIF TG_TABLE_NAME = 'nodes' THEN
        IF NOT NEW.enabled OR NEW.host IS DISTINCT FROM OLD.host
            OR NEW.group_id IS DISTINCT FROM OLD.group_id
            OR NEW.relay_port IS DISTINCT FROM OLD.relay_port
            OR NOT ('forward' = ANY(NEW.capabilities)) THEN
            DELETE FROM agent_relay_certificate_grants WHERE node_id = NEW.id;
        END IF;
    ELSE
        IF NOT NEW.enabled THEN
            DELETE FROM agent_relay_certificate_grants WHERE node_id IN
                (SELECT id FROM nodes WHERE group_id = NEW.id);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER agents_relay_certificate_invalidate AFTER UPDATE OF status,cert_fingerprint ON agents
FOR EACH ROW EXECUTE FUNCTION invalidate_agent_relay_certificates();
CREATE TRIGGER nodes_relay_certificate_invalidate AFTER UPDATE OF enabled,host,group_id,relay_port,capabilities ON nodes
FOR EACH ROW EXECUTE FUNCTION invalidate_agent_relay_certificates();
CREATE TRIGGER groups_relay_certificate_invalidate AFTER UPDATE OF enabled ON resource_groups
FOR EACH ROW EXECUTE FUNCTION invalidate_agent_relay_certificates();
