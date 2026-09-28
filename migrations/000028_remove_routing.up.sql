-- Preserve subscriptions and proxy targets while removing policy bindings.
-- The installer backs up the database before applying migrations.
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_routing_profile_owner_fk;
DROP INDEX subscriptions_routing_profile_idx;
ALTER TABLE subscriptions DROP COLUMN routing_profile_id;

DROP TABLE routing_rules;
DROP TABLE routing_profiles;
DROP TABLE routing_rule_sets;
ALTER TABLE plan_limits DROP COLUMN max_routing_rules;

DELETE FROM role_permissions
WHERE permission_code IN ('routing.read','routing.write','routing_rulesets.read','routing_rulesets.write');
DELETE FROM permissions
WHERE code IN ('routing.read','routing.write','routing_rulesets.read','routing_rulesets.write');
