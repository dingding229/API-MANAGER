DROP INDEX IF EXISTS idx_plugins_name_enabled;
ALTER TABLE plugins
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS storage_path,
    DROP COLUMN IF EXISTS checksum;
