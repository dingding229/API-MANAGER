ALTER TABLE apis ADD COLUMN IF NOT EXISTS public_test_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS session_id TEXT;
UPDATE user_sessions SET session_id=gen_random_uuid()::text WHERE session_id IS NULL;
ALTER TABLE user_sessions ALTER COLUMN session_id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS user_sessions_id_idx ON user_sessions(session_id);
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS login_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS last_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS peer_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS ip_source TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS device TEXT NOT NULL DEFAULT '未知设备';
CREATE TABLE IF NOT EXISTS api_test_tickets (
 ticket_hash TEXT PRIMARY KEY,
 session_hash TEXT NOT NULL REFERENCES user_sessions(key_hash) ON DELETE CASCADE,
 api_id TEXT NOT NULL REFERENCES apis(id) ON DELETE CASCADE,
 request_digest TEXT NOT NULL,
 client_ip TEXT NOT NULL,
 user_agent TEXT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS api_test_tickets_expiry_idx ON api_test_tickets(expires_at);
