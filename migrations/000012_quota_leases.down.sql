ALTER TABLE usage_events DROP COLUMN IF EXISTS lease_id;
ALTER TABLE usage_sessions DROP COLUMN IF EXISTS first_lease_id;
DROP TABLE IF EXISTS quota_leases;
ALTER TABLE billing_periods DROP COLUMN IF EXISTS reserved_bytes;
ALTER TABLE billing_periods DROP COLUMN IF EXISTS activated_at;
ALTER TABLE memberships DROP COLUMN IF EXISTS initial_snapshot_json;
