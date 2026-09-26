DROP TRIGGER IF EXISTS usage_events_immutable ON usage_events;
DROP FUNCTION IF EXISTS reject_usage_event_change();
DROP TABLE IF EXISTS usage_events;
DROP TABLE IF EXISTS usage_sessions;
DROP TABLE IF EXISTS billing_periods;
ALTER TABLE memberships DROP COLUMN IF EXISTS period_months;
-- btree_gist may be shared by other database objects and is deliberately retained.
