DROP INDEX IF EXISTS idx_role_permissions_role_id;
DROP INDEX IF EXISTS idx_user_roles_user_id;
ALTER TABLE roles DROP COLUMN IF EXISTS description;
