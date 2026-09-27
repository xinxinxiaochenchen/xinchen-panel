ALTER TABLE nodes ADD COLUMN relay_port integer;
ALTER TABLE nodes ADD CONSTRAINT nodes_relay_port_valid CHECK (
    relay_port IS NULL OR (
        relay_port BETWEEN 1024 AND 65535
        AND 'forward' = ANY(capabilities)
        AND relay_port IS DISTINCT FROM proxy_port
    )
);
CREATE UNIQUE INDEX nodes_relay_endpoint_uniq ON nodes (lower(host), relay_port)
WHERE relay_port IS NOT NULL;

-- Reserve public TCP endpoints across both proxy and relay listeners. A
-- unique index on each nullable node column alone misses cross-kind clashes.
CREATE TABLE node_tcp_endpoints (
    node_id uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('proxy', 'relay')),
    host text NOT NULL,
    port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    PRIMARY KEY (node_id, kind)
);
CREATE UNIQUE INDEX node_tcp_endpoints_unique ON node_tcp_endpoints (lower(host), port);
INSERT INTO node_tcp_endpoints(node_id,kind,host,port)
SELECT id,'proxy',host,proxy_port FROM nodes WHERE proxy_port IS NOT NULL;

CREATE FUNCTION sync_node_tcp_endpoints() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM node_tcp_endpoints WHERE node_id = NEW.id;
    IF NEW.proxy_port IS NOT NULL THEN
        INSERT INTO node_tcp_endpoints(node_id,kind,host,port)
        VALUES (NEW.id,'proxy',NEW.host,NEW.proxy_port);
    END IF;
    IF NEW.relay_port IS NOT NULL THEN
        INSERT INTO node_tcp_endpoints(node_id,kind,host,port)
        VALUES (NEW.id,'relay',NEW.host,NEW.relay_port);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER nodes_tcp_endpoints_sync
AFTER INSERT OR UPDATE OF host,proxy_port,relay_port ON nodes
FOR EACH ROW EXECUTE FUNCTION sync_node_tcp_endpoints();

ALTER TABLE port_allocations DROP CONSTRAINT port_allocations_owner_type_check;
ALTER TABLE port_allocations ADD CONSTRAINT port_allocations_owner_type_check
CHECK (owner_type IN ('forward_rule', 'node_relay'));

-- Reserve both transports, including while the node is disabled, so future
-- UDP relay support cannot silently take a user-owned port. The existing
-- unique allocation index is the concurrency boundary for both owner types.
CREATE FUNCTION reserve_node_relay_port() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM port_allocations WHERE node_id = OLD.id AND owner_type = 'node_relay';
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.relay_port IS NOT DISTINCT FROM OLD.relay_port THEN
        RETURN NEW;
    END IF;
    DELETE FROM port_allocations WHERE node_id = NEW.id AND owner_type = 'node_relay';
    IF NEW.relay_port IS NOT NULL THEN
        INSERT INTO port_allocations(id,node_id,protocol,port,owner_type,owner_id)
        VALUES (gen_random_uuid(),NEW.id,'TCP',NEW.relay_port,'node_relay',NEW.id),
               (gen_random_uuid(),NEW.id,'UDP',NEW.relay_port,'node_relay',NEW.id);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER nodes_relay_port_reserve
AFTER INSERT OR UPDATE OF relay_port ON nodes
FOR EACH ROW EXECUTE FUNCTION reserve_node_relay_port();
CREATE TRIGGER nodes_relay_port_release
BEFORE DELETE ON nodes
FOR EACH ROW EXECUTE FUNCTION reserve_node_relay_port();
