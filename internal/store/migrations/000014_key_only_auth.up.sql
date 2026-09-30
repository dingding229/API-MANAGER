-- Normalize existing routes to KEY protection. Historical snapshots remain
-- unchanged; unsupported snapshots are rejected by rollback validation.
UPDATE apis SET auth_mode = 'api_key', auth_config = '{}'::jsonb
WHERE auth_mode <> 'api_key' OR auth_config <> '{}'::jsonb;
ALTER TABLE apis ALTER COLUMN auth_mode SET DEFAULT 'api_key';
