ALTER TABLE api_credentials
    ADD COLUMN IF NOT EXISTS encrypted_key TEXT NOT NULL DEFAULT '';
