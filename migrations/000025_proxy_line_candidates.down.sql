DROP INDEX IF EXISTS proxy_access_lines_line_idx;
DROP TABLE IF EXISTS proxy_access_lines;
ALTER TABLE plan_limits DROP CONSTRAINT IF EXISTS plan_limits_max_proxy_lines_check;
ALTER TABLE plan_limits DROP COLUMN IF EXISTS max_proxy_lines;
