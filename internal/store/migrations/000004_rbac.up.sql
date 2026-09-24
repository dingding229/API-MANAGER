ALTER TABLE roles ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_user_roles_user_id ON user_roles(user_id);
CREATE INDEX IF NOT EXISTS idx_role_permissions_role_id ON role_permissions(role_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_roles_global_name ON roles ((COALESCE(tenant_id::text, '')), name) WHERE tenant_id IS NULL;
