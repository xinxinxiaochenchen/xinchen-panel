ALTER TABLE agents DROP CONSTRAINT agents_capabilities_check;
ALTER TABLE agents ADD CONSTRAINT agents_capabilities_check CHECK (
    capabilities <@ ARRAY['forward','proxy','relay']::text[]
    AND cardinality(capabilities) BETWEEN 1 AND 3
);
