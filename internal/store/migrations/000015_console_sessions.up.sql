CREATE TABLE IF NOT EXISTS user_sessions (
    key_hash TEXT PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_user_sessions_expiry ON user_sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_user_sessions_user ON user_sessions(user_id);
