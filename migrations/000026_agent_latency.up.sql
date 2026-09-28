ALTER TABLE agents
    ADD COLUMN latency_ms integer,
    ADD COLUMN latency_observed_at timestamptz;

ALTER TABLE agents
    ADD CONSTRAINT agents_latency_ms_check CHECK (latency_ms IS NULL OR latency_ms >= 0);
