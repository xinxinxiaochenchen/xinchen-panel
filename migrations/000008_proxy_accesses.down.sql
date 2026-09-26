DROP TABLE proxy_accesses;
DELETE FROM permissions WHERE code IN ('proxy_accesses.read', 'proxy_accesses.write');
