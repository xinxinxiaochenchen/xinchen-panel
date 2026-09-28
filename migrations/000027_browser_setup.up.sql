CREATE TABLE installation_setup (
    id smallint PRIMARY KEY CHECK (id = 1),
    completed_at timestamptz
);
INSERT INTO installation_setup (id, completed_at)
SELECT 1, CASE WHEN EXISTS (SELECT 1 FROM user_roles WHERE role_code = 'admin')
    THEN clock_timestamp() ELSE NULL END;
