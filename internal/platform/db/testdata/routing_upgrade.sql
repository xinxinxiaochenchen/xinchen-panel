-- A version 27 installation with subscriptions, candidate lines, frozen grants,
-- routing policies and custom roles. Used only inside an isolated test schema.
INSERT INTO users(id,email,password_hash,status) VALUES
('00000000-0000-7000-8000-000000000001','routing-upgrade@example.invalid','test-hash','active');
INSERT INTO resource_groups(id,code,name,region) VALUES
('00000000-0000-7000-8000-000000000002','UPGRADE','Upgrade group','JP');
INSERT INTO nodes(id,group_id,name,region,host,proxy_port,capabilities) VALUES
('00000000-0000-7000-8000-000000000003','00000000-0000-7000-8000-000000000002','Upgrade node','JP','upgrade.example.invalid',443,ARRAY['proxy']);
INSERT INTO lines(id,name,created_by) VALUES
('00000000-0000-7000-8000-000000000004','Upgrade line','00000000-0000-7000-8000-000000000001');
INSERT INTO line_hops(line_id,position,node_id,role) VALUES
('00000000-0000-7000-8000-000000000004',0,'00000000-0000-7000-8000-000000000003','egress');
INSERT INTO plans(id,name,quota_bytes) VALUES
('00000000-0000-7000-8000-000000000005','Upgrade plan',1000000);
INSERT INTO plan_limits(plan_id,max_subscriptions,max_forward_rules_per_node,max_routing_rules,allow_custom_lines,max_custom_lines,max_hops,max_proxy_lines) VALUES
('00000000-0000-7000-8000-000000000005',3,4,20,true,2,3,4);
INSERT INTO memberships(id,user_id,plan_id,starts_at,ends_at,status,anchor_day,timezone,snapshot_json) VALUES
('00000000-0000-7000-8000-000000000006','00000000-0000-7000-8000-000000000001','00000000-0000-7000-8000-000000000005',now(),now()+interval '1 month','active',1,'UTC',
'{"plan_name":"Upgrade plan","quota_bytes":1000000,"limits":{"max_subscriptions":3,"max_forward_rules_per_node":4,"max_routing_rules":20,"allow_custom_lines":true,"max_custom_lines":2,"max_hops":3,"max_proxy_lines":4}}');
INSERT INTO proxy_accesses(id,user_id,line_id,name,credential_hash,credential_ciphertext,apply_status) VALUES
('00000000-0000-7000-8000-000000000007','00000000-0000-7000-8000-000000000001','00000000-0000-7000-8000-000000000004','Upgrade proxy',repeat('a',56),repeat('c',32),'active');
INSERT INTO proxy_access_lines(proxy_access_id,line_id,position) VALUES
('00000000-0000-7000-8000-000000000007','00000000-0000-7000-8000-000000000004',0);
INSERT INTO routing_profiles(id,user_id,name,fallback_kind,fallback_line_id) VALUES
('00000000-0000-7000-8000-000000000008','00000000-0000-7000-8000-000000000001','Upgrade profile','line','00000000-0000-7000-8000-000000000004');
INSERT INTO routing_rules(id,profile_id,priority,match_type,match_value,action,line_id) VALUES
('00000000-0000-7000-8000-000000000009','00000000-0000-7000-8000-000000000008',1,'domain','example.com','line','00000000-0000-7000-8000-000000000004');
INSERT INTO routing_rule_sets(id,kind,code,name,version,source,sha256,entries) VALUES
('00000000-0000-7000-8000-000000000010','geosite','cn','Upgrade rules','v1','test fixture',repeat('b',64),'["suffix:example.com"]');
INSERT INTO subscriptions(id,user_id,name,token_hash,token_ciphertext,routing_profile_id) VALUES
('00000000-0000-7000-8000-000000000011','00000000-0000-7000-8000-000000000001','Bound subscription',repeat('a',64),repeat('s',32),'00000000-0000-7000-8000-000000000008'),
('00000000-0000-7000-8000-000000000012','00000000-0000-7000-8000-000000000001','Plain subscription',repeat('b',64),repeat('t',32),NULL);
INSERT INTO subscription_proxy_targets(subscription_id,user_id,proxy_access_id,sort_order) VALUES
('00000000-0000-7000-8000-000000000011','00000000-0000-7000-8000-000000000001','00000000-0000-7000-8000-000000000007',0),
('00000000-0000-7000-8000-000000000012','00000000-0000-7000-8000-000000000001','00000000-0000-7000-8000-000000000007',0);
INSERT INTO roles(code,description) VALUES ('upgrade-custom','Upgrade custom role');
INSERT INTO role_permissions(role_code,permission_code)
SELECT 'upgrade-custom',code FROM permissions WHERE code IN
('routing.read','routing.write','routing_rulesets.read','routing_rulesets.write','subscriptions.read');
