ALTER TABLE lines ADD COLUMN relay_generation bigint NOT NULL DEFAULT 1 CHECK (relay_generation > 0);

CREATE TABLE line_relay_generations (
    line_id uuid NOT NULL REFERENCES lines(id) ON DELETE CASCADE,
    generation bigint NOT NULL CHECK (generation > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (line_id, generation)
);

CREATE TABLE line_relay_secrets (
    line_id uuid NOT NULL,
    generation bigint NOT NULL,
    edge_position integer NOT NULL CHECK (edge_position >= 0),
    secret_ciphertext bytea NOT NULL CHECK (octet_length(secret_ciphertext) >= 44),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (line_id, generation, edge_position),
    FOREIGN KEY (line_id, generation) REFERENCES line_relay_generations(line_id, generation) ON DELETE CASCADE
);
