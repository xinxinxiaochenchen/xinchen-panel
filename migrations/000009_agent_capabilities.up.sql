ALTER TABLE agents ADD COLUMN capabilities text[] NOT NULL DEFAULT ARRAY['forward']::text[]
    CHECK (capabilities <@ ARRAY['forward','proxy']::text[] AND cardinality(capabilities) BETWEEN 1 AND 2);
