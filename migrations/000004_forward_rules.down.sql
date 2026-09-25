DROP TABLE IF EXISTS port_allocations;
DROP TABLE IF EXISTS forward_rules;
DROP TABLE IF EXISTS forward_target_policies;
DELETE FROM permissions WHERE code = 'forward_policies.write';
