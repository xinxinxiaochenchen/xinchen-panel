ALTER TABLE agents DROP CONSTRAINT agents_latency_ms_check;
ALTER TABLE agents DROP COLUMN latency_observed_at, DROP COLUMN latency_ms;
