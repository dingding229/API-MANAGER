CREATE TABLE plugin_runtime_policies (
 plugin_id TEXT PRIMARY KEY REFERENCES plugins(id) ON DELETE CASCADE,
 policy JSONB NOT NULL,
 version BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE SEQUENCE plugin_session_version_seq;
CREATE TABLE plugin_sessions (
 plugin_id TEXT NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
 key_hash TEXT NOT NULL CHECK (length(key_hash)=64),
 encrypted_value TEXT NOT NULL,
 version BIGINT NOT NULL DEFAULT nextval('plugin_session_version_seq'),
 expires_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(plugin_id,key_hash)
);
CREATE INDEX plugin_sessions_expiry_idx ON plugin_sessions(expires_at) WHERE expires_at IS NOT NULL;
