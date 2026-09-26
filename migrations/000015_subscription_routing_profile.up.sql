ALTER TABLE subscriptions ADD COLUMN routing_profile_id uuid;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_routing_profile_owner_fk
    FOREIGN KEY (routing_profile_id,user_id) REFERENCES routing_profiles(id,user_id) ON DELETE RESTRICT;
CREATE INDEX subscriptions_routing_profile_idx ON subscriptions(routing_profile_id) WHERE routing_profile_id IS NOT NULL;
