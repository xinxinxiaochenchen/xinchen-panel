ALTER TABLE plan_limits
    ADD COLUMN max_proxy_lines integer NOT NULL DEFAULT 1
    CONSTRAINT plan_limits_max_proxy_lines_check CHECK (max_proxy_lines BETWEEN 1 AND 32);

CREATE TABLE proxy_access_lines (
    proxy_access_id uuid NOT NULL REFERENCES proxy_accesses(id) ON DELETE CASCADE,
    line_id uuid NOT NULL REFERENCES lines(id),
    position integer NOT NULL CHECK (position >= 0),
    priority integer NOT NULL DEFAULT 100 CHECK (priority BETWEEN 0 AND 1000),
    weight integer NOT NULL DEFAULT 1 CHECK (weight BETWEEN 1 AND 100),
    PRIMARY KEY (proxy_access_id, line_id),
    UNIQUE (proxy_access_id, position)
);
CREATE INDEX proxy_access_lines_line_idx ON proxy_access_lines(line_id, proxy_access_id);

INSERT INTO proxy_access_lines(proxy_access_id, line_id, position)
SELECT id, line_id, 0 FROM proxy_accesses;
