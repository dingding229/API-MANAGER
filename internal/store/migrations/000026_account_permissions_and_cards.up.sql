INSERT INTO permissions(id,code,description) VALUES
(gen_random_uuid(),'account.profile','修改个人资料'),
(gen_random_uuid(),'account.security','管理个人账号安全'),
(gen_random_uuid(),'account.keys.read','查看个人调用凭据'),
(gen_random_uuid(),'account.keys.write','创建、重置和吊销个人调用凭据'),
(gen_random_uuid(),'account.keys.reveal','查看个人调用密钥'),
(gen_random_uuid(),'account.logs','查看个人调用日志'),
(gen_random_uuid(),'account.sessions','管理个人登录会话'),
(gen_random_uuid(),'account.billing.read','查看个人余额、套餐和用量'),
(gen_random_uuid(),'account.billing.purchase','购买与续订套餐'),
(gen_random_uuid(),'account.billing.redeem','兑换余额卡密和套餐卡密'),
(gen_random_uuid(),'billing.read','查看全站余额和套餐'),
(gen_random_uuid(),'billing.manage','管理套餐、绑定套餐和调整余额'),
(gen_random_uuid(),'card.manage','创建、查看和停用卡密')
ON CONFLICT(code) DO NOTHING;
INSERT INTO role_permissions(role_id,permission_id) SELECT r.id,p.id FROM roles r CROSS JOIN permissions p WHERE r.name IN ('member','api_developer') AND p.code IN ('account.profile','account.security','account.keys.read','account.keys.write','account.keys.reveal','account.logs','account.sessions','account.billing.read','account.billing.purchase','account.billing.redeem') ON CONFLICT DO NOTHING;
CREATE TABLE card_batches (
 id TEXT PRIMARY KEY, creator_id UUID NOT NULL REFERENCES users(id), operation_id TEXT NOT NULL,
 request_hash TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('balance','plan')),
 amount_micros BIGINT NOT NULL DEFAULT 0 CHECK(amount_micros BETWEEN 0 AND 1000000000000000), plan_snapshot JSONB, count INTEGER NOT NULL CHECK(count BETWEEN 1 AND 100),
 expires_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), UNIQUE(creator_id,operation_id)
);
CREATE TABLE redeem_cards (
 id TEXT PRIMARY KEY, batch_id TEXT NOT NULL REFERENCES card_batches(id), code_hash TEXT NOT NULL UNIQUE,
 prefix TEXT NOT NULL, encrypted_code TEXT NOT NULL, revoked BOOLEAN NOT NULL DEFAULT FALSE,
 redeemed_by UUID REFERENCES users(id), redeemed_at TIMESTAMPTZ
);
CREATE INDEX redeem_cards_batch_idx ON redeem_cards(batch_id);
CREATE TABLE plugin_settings (
 plugin_id TEXT PRIMARY KEY REFERENCES plugins(id) ON DELETE CASCADE,
 encrypted_settings TEXT NOT NULL, version BIGINT NOT NULL DEFAULT 1, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
