ALTER TABLE proxy_accesses ADD CONSTRAINT proxy_accesses_id_user_unique UNIQUE (id,user_id);

CREATE TABLE subscriptions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    enabled boolean NOT NULL DEFAULT true,
    token_hash char(64) NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    token_ciphertext text NOT NULL CHECK (char_length(token_ciphertext) BETWEEN 32 AND 512),
    name_template text NOT NULL DEFAULT '{region} · {name}' CHECK (char_length(name_template) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT subscriptions_owner_name_unique UNIQUE (user_id,name),
    CONSTRAINT subscriptions_id_user_unique UNIQUE (id,user_id)
);
CREATE INDEX subscriptions_user_id_idx ON subscriptions(user_id,id);

CREATE TABLE subscription_proxy_targets (
    subscription_id uuid NOT NULL,
    user_id uuid NOT NULL,
    proxy_access_id uuid NOT NULL,
    sort_order integer NOT NULL CHECK (sort_order BETWEEN 0 AND 99),
    PRIMARY KEY (subscription_id,proxy_access_id),
    UNIQUE (subscription_id,sort_order),
    FOREIGN KEY (subscription_id,user_id) REFERENCES subscriptions(id,user_id) ON DELETE CASCADE,
    FOREIGN KEY (proxy_access_id,user_id) REFERENCES proxy_accesses(id,user_id) ON DELETE CASCADE
);
CREATE INDEX subscription_proxy_targets_access_idx ON subscription_proxy_targets(proxy_access_id);
