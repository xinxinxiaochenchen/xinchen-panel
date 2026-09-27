DROP INDEX IF EXISTS forward_rules_line_idx;
ALTER TABLE forward_rules DROP CONSTRAINT IF EXISTS forward_rules_line_target_check;
ALTER TABLE forward_rules ADD CONSTRAINT forward_rules_direct_only CHECK (line_id IS NULL);
