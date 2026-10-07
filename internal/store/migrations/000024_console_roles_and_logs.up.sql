ALTER TABLE roles ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE TABLE IF NOT EXISTS retired_role_settings (
 role_id UUID PRIMARY KEY,
 snapshot JSONB NOT NULL,
 retired_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Keep an immutable record of old permissions and memberships before consolidation.
INSERT INTO retired_role_settings(role_id,snapshot)
SELECT r.id,jsonb_build_object('role',to_jsonb(r),'permissions',
 COALESCE((SELECT jsonb_agg(p.code) FROM role_permissions rp JOIN permissions p ON p.id=rp.permission_id WHERE rp.role_id=r.id),'[]'::jsonb),
 'users',COALESCE((SELECT jsonb_agg(ur.user_id) FROM user_roles ur WHERE ur.role_id=r.id),'[]'::jsonb))
FROM roles r WHERE r.tenant_id IS NULL ON CONFLICT DO NOTHING;
INSERT INTO roles(id,name,description,display_name)
SELECT gen_random_uuid(),v.name,v.description,v.display_name
FROM (VALUES ('super_admin','管理所有用户、接口与网站设置','管理员'),('member','使用个人账户、调用凭据和公开在线测试','普通用户'),('api_developer','开发、发布和管理接口','接口开发者')) v(name,description,display_name)
WHERE NOT EXISTS(SELECT 1 FROM roles r WHERE r.tenant_id IS NULL AND r.name=v.name);
-- Former privileged-but-not-owner roles are not promoted to the administrator role.
INSERT INTO user_roles(user_id,role_id)
SELECT DISTINCT ur.user_id,target.id FROM user_roles ur JOIN roles old ON old.id=ur.role_id
JOIN roles target ON target.tenant_id IS NULL AND target.name=CASE WHEN old.name IN ('tenant_admin','operator') THEN 'api_developer' ELSE 'member' END
WHERE old.tenant_id IS NULL AND old.name NOT IN ('super_admin','member','api_developer') ON CONFLICT DO NOTHING;
UPDATE users SET role=CASE WHEN role IN ('tenant_admin','operator') THEN 'api_developer' ELSE 'member' END,
 auth_revision=auth_revision+1,updated_at=NOW() WHERE role NOT IN ('super_admin','member','api_developer');
DELETE FROM roles WHERE tenant_id IS NULL AND name NOT IN ('super_admin','member','api_developer');
UPDATE roles SET display_name=CASE name WHEN 'super_admin' THEN '管理员' WHEN 'member' THEN '普通用户' ELSE '接口开发者' END WHERE tenant_id IS NULL AND display_name IN ('','超级管理员','普通会员','普通用户','只读用户','接口开发者');
-- Remove administrative read access from the ordinary-user default and global credentials from the developer default.
DELETE FROM role_permissions rp USING roles r,permissions p WHERE rp.role_id=r.id AND rp.permission_id=p.id AND r.tenant_id IS NULL
AND ((r.name='member' AND p.code NOT IN ('api.test','api.test.write')) OR (r.name='api_developer' AND p.code IN ('credential.read','credential.reveal','credential.write','user.read','user.manage','user.sessions.manage','audit.read')));
INSERT INTO permissions(id,code,description) VALUES(gen_random_uuid(),'api.test.write','执行非只读在线测试') ON CONFLICT DO NOTHING;
INSERT INTO permissions(id,code,description) VALUES
(gen_random_uuid(),'api.read','查看接口与 OpenAPI 文档'),
(gen_random_uuid(),'api.write','创建与修改接口'),
(gen_random_uuid(),'api.publish','发布、下线与回滚接口'),
(gen_random_uuid(),'api.delete','删除接口'),
(gen_random_uuid(),'credential.read','查看调用凭证列表'),
(gen_random_uuid(),'credential.reveal','查看完整调用密钥'),
(gen_random_uuid(),'credential.write','创建与吊销调用凭证'),
(gen_random_uuid(),'plugin.read','查看插件'),
(gen_random_uuid(),'plugin.manage','管理插件配置'),
(gen_random_uuid(),'api.test.write','执行非只读在线测试'),
(gen_random_uuid(),'api.test','在公开文档中执行在线测试'),
(gen_random_uuid(),'user.sessions.manage','查看与退出其他用户的登录会话'),
(gen_random_uuid(),'user.read','查看用户、角色与权限'),
(gen_random_uuid(),'user.manage','创建用户与分配角色'),
(gen_random_uuid(),'audit.read','查看审计日志'),
(gen_random_uuid(),'observability.read','查看内置指标、日志、链路与告警'),
(gen_random_uuid(),'observability.manage','确认和管理内置告警'),
(gen_random_uuid(),'observability.logs.clear','清理全部应用日志（不包含审计日志）') ON CONFLICT DO NOTHING;
INSERT INTO role_permissions(role_id,permission_id) SELECT r.id,p.id FROM roles r CROSS JOIN permissions p WHERE r.tenant_id IS NULL AND r.name IN ('member','api_developer') AND p.code IN ('api.test','api.test.write') ON CONFLICT DO NOTHING;
UPDATE authentication_settings SET settings=jsonb_set(settings,'{default_role}','"member"'),version=version+1 WHERE COALESCE(settings->>'default_role','member')<>'member';
-- Visible, published interfaces are the single source of truth for online tests.
UPDATE apis SET public_test_enabled=public_visible,rate_limit_per_minute=0,daily_quota=0,monthly_quota=0,updated_at=NOW();
DELETE FROM api_test_tickets;
ALTER TABLE user_call_logs ADD COLUMN IF NOT EXISTS request_id TEXT NOT NULL DEFAULT '';
ALTER TABLE user_call_logs ADD COLUMN IF NOT EXISTS trace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE user_call_logs ADD COLUMN IF NOT EXISTS credential_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS user_call_logs_user_status_idx ON user_call_logs(user_id,status,created_at DESC);

INSERT INTO role_permissions(role_id,permission_id) SELECT r.id,p.id FROM roles r CROSS JOIN permissions p WHERE r.tenant_id IS NULL AND r.name='api_developer' AND NOT EXISTS(SELECT 1 FROM retired_role_settings backup WHERE backup.role_id=r.id) AND p.code IN ('api.read','api.write','api.publish','api.delete','plugin.read','plugin.manage','observability.read') ON CONFLICT DO NOTHING;

INSERT INTO role_permissions(role_id,permission_id) SELECT rp.role_id,target.id FROM role_permissions rp JOIN permissions old ON old.id=rp.permission_id CROSS JOIN permissions target WHERE old.code='api.test.write' AND target.code='api.test' ON CONFLICT DO NOTHING;
DELETE FROM permissions WHERE code='api.test.write';
DELETE FROM permissions WHERE code='user.sessions.manage';
