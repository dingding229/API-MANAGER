DROP INDEX IF EXISTS idx_audit_logs_actor_created;
DROP INDEX IF EXISTS idx_audit_logs_resource_created;
DROP INDEX IF EXISTS idx_audit_logs_action_created;
DROP INDEX IF EXISTS idx_audit_logs_created_at;
ALTER TABLE audit_logs
    DROP COLUMN IF EXISTS status_code,
    DROP COLUMN IF EXISTS user_agent,
    DROP COLUMN IF EXISTS remote_addr,
    DROP COLUMN IF EXISTS path,
    DROP COLUMN IF EXISTS method,
    DROP COLUMN IF EXISTS actor_email,
    DROP COLUMN IF EXISTS actor_type;
