ALTER TABLE forward_rules DROP CONSTRAINT IF EXISTS forward_rules_direct_only;
ALTER TABLE forward_rules ADD CONSTRAINT forward_rules_line_target_check CHECK (line_id IS NULL OR target_node_id IS NULL);
CREATE INDEX forward_rules_line_idx ON forward_rules(line_id) WHERE line_id IS NOT NULL;
