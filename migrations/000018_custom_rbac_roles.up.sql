ALTER TABLE roles ADD COLUMN id uuid;
UPDATE roles SET id = gen_random_uuid() WHERE id IS NULL;
ALTER TABLE roles ALTER COLUMN id SET NOT NULL;
ALTER TABLE roles ALTER COLUMN id SET DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX roles_id_uniq ON roles(id);

INSERT INTO permissions(code,description) VALUES
    ('roles.read','View roles and permission catalog'),
    ('roles.write','Manage custom roles and user role assignments');
INSERT INTO role_permissions(role_code,permission_code)
SELECT 'admin',code FROM permissions WHERE code IN ('roles.read','roles.write');
