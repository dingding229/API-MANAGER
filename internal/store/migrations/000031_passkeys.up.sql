CREATE TABLE user_passkeys (
 credential_id TEXT PRIMARY KEY CHECK (length(credential_id) BETWEEN 1 AND 2048),
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 rp_id TEXT NOT NULL CHECK (length(rp_id) BETWEEN 1 AND 253),
 name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
 credential JSONB NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 last_used_at TIMESTAMPTZ
);
CREATE INDEX user_passkeys_owner_idx ON user_passkeys(user_id, rp_id);
