DROP TABLE routing_rule_sets;
DELETE FROM role_permissions WHERE permission_code IN ('routing_rulesets.read', 'routing_rulesets.write');
DELETE FROM permissions WHERE code IN ('routing_rulesets.read', 'routing_rulesets.write');
