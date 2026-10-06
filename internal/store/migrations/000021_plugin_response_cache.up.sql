ALTER TABLE apis ADD COLUMN IF NOT EXISTS plugin_cache JSONB NOT NULL DEFAULT '{}'::JSONB;
CREATE TABLE IF NOT EXISTS plugin_response_cache (
 api_id TEXT NOT NULL REFERENCES apis(id) ON DELETE CASCADE,
 cache_key TEXT NOT NULL,
 encrypted_response TEXT NOT NULL,
 api_updated_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY (api_id, cache_key)
);
CREATE INDEX IF NOT EXISTS plugin_response_cache_expiry_idx ON plugin_response_cache(expires_at);
