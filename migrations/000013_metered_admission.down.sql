DROP TABLE IF EXISTS connection_lease_bindings;
DROP INDEX IF EXISTS usage_sessions_resource_idx;
ALTER TABLE usage_sessions DROP CONSTRAINT IF EXISTS usage_sessions_resource_binding;
ALTER TABLE usage_sessions DROP COLUMN IF EXISTS config_revision;
ALTER TABLE usage_sessions DROP COLUMN IF EXISTS resource_id;
ALTER TABLE usage_sessions DROP COLUMN IF EXISTS resource_kind;
