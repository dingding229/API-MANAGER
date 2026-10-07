ALTER TABLE apis ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT '';
CREATE INDEX apis_owner_idx ON apis(owner_user_id);
-- Recover only creation actors proven by existing audit records; never guess legacy owners.
UPDATE apis a SET owner_user_id=x.actor_id::text FROM (
 SELECT DISTINCT ON(resource_id) resource_id,actor_id FROM audit_logs
 WHERE action='api.create' AND actor_id IS NOT NULL ORDER BY resource_id,created_at ASC
) x WHERE a.id=x.resource_id AND a.owner_user_id='';
ALTER TABLE api_credentials ADD COLUMN allowed_ip_ranges JSONB NOT NULL DEFAULT '[]';
ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ;
CREATE INDEX users_visible_created_idx ON users(created_at) WHERE deleted_at IS NULL;
CREATE TABLE version_check_settings (
 singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
 encrypted_token TEXT NOT NULL DEFAULT '', version BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO permissions(id,code,description) VALUES(gen_random_uuid(),'user.delete','删除用户（保留账务与审计）') ON CONFLICT(code) DO NOTHING;
