DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM roles WHERE code NOT IN ('admin','user')) THEN
        RAISE EXCEPTION 'custom roles must be removed before migration 18 can be rolled back';
    END IF;
END $$;
DELETE FROM role_permissions WHERE permission_code IN ('roles.read','roles.write');
DELETE FROM permissions WHERE code IN ('roles.read','roles.write');
DROP INDEX roles_id_uniq;
ALTER TABLE roles DROP COLUMN id;
