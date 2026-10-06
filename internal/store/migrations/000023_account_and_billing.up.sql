ALTER TABLE users ADD COLUMN IF NOT EXISTS nickname TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE roles ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '';
UPDATE roles SET display_name=CASE name WHEN 'super_admin' THEN '超级管理员' WHEN 'tenant_admin' THEN '平台管理员' WHEN 'operator' THEN '运维人员' WHEN 'api_developer' THEN '接口开发者' WHEN 'viewer' THEN '普通用户' ELSE name END WHERE display_name='';
ALTER TABLE apis ADD COLUMN IF NOT EXISTS price_micros BIGINT NOT NULL DEFAULT 0 CHECK(price_micros>=0);
ALTER TABLE api_credentials ADD COLUMN IF NOT EXISTS owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE TABLE IF NOT EXISTS wallets(user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,balance_micros BIGINT NOT NULL DEFAULT 0 CHECK(balance_micros>=0),held_micros BIGINT NOT NULL DEFAULT 0 CHECK(held_micros>=0 AND held_micros<=balance_micros));
CREATE TABLE IF NOT EXISTS wallet_ledger(id TEXT PRIMARY KEY,user_id UUID NOT NULL REFERENCES users(id),kind TEXT NOT NULL,reference TEXT NOT NULL UNIQUE,note TEXT NOT NULL DEFAULT '',amount_micros BIGINT NOT NULL,balance_micros BIGINT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE INDEX IF NOT EXISTS wallet_ledger_user_idx ON wallet_ledger(user_id,created_at DESC);
CREATE TABLE IF NOT EXISTS plans(id TEXT PRIMARY KEY,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',price_micros BIGINT NOT NULL CHECK(price_micros>=0),days INTEGER NOT NULL CHECK(days BETWEEN 1 AND 366),hourly BIGINT NOT NULL,daily BIGINT NOT NULL,monthly BIGINT NOT NULL,enabled BOOLEAN NOT NULL DEFAULT TRUE,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS subscriptions(id TEXT PRIMARY KEY,user_id UUID NOT NULL REFERENCES users(id),plan_id TEXT NOT NULL,plan_name TEXT NOT NULL,hourly BIGINT NOT NULL,daily BIGINT NOT NULL,monthly BIGINT NOT NULL,starts_at TIMESTAMPTZ NOT NULL,expires_at TIMESTAMPTZ NOT NULL);
CREATE INDEX IF NOT EXISTS subscriptions_user_idx ON subscriptions(user_id,expires_at);
CREATE TABLE IF NOT EXISTS usage_windows(user_id UUID NOT NULL REFERENCES users(id),subscription_id TEXT NOT NULL,window_kind TEXT NOT NULL,window_start TIMESTAMPTZ NOT NULL,used BIGINT NOT NULL DEFAULT 0,PRIMARY KEY(user_id,subscription_id,window_kind,window_start));
CREATE TABLE IF NOT EXISTS api_charges(id TEXT PRIMARY KEY,user_id UUID NOT NULL REFERENCES users(id),api_id TEXT NOT NULL,price_micros BIGINT NOT NULL,subscription_id TEXT NOT NULL DEFAULT '',outcome TEXT NOT NULL DEFAULT 'held',created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS user_call_logs(id TEXT PRIMARY KEY,user_id UUID NOT NULL REFERENCES users(id),api_id TEXT NOT NULL,api_name TEXT NOT NULL,method TEXT NOT NULL,path TEXT NOT NULL,client_ip TEXT NOT NULL,status INTEGER NOT NULL,price_micros BIGINT NOT NULL,duration_ms BIGINT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
CREATE INDEX IF NOT EXISTS user_call_logs_user_idx ON user_call_logs(user_id,created_at DESC);
CREATE TABLE IF NOT EXISTS account_security(user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,totp_secret TEXT NOT NULL DEFAULT '',totp_pending TEXT NOT NULL DEFAULT '',totp_last_step BIGINT NOT NULL DEFAULT -1,recovery_hashes JSONB NOT NULL DEFAULT '[]');
CREATE TABLE IF NOT EXISTS account_verifications(id TEXT PRIMARY KEY,purpose TEXT NOT NULL,subject TEXT NOT NULL,user_id TEXT NOT NULL DEFAULT '',code_hash TEXT NOT NULL,payload TEXT NOT NULL DEFAULT '',binding TEXT NOT NULL DEFAULT '',attempts INTEGER NOT NULL DEFAULT 0,expires_at TIMESTAMPTZ NOT NULL);
CREATE INDEX IF NOT EXISTS account_verifications_expiry_idx ON account_verifications(expires_at);
CREATE TABLE IF NOT EXISTS external_identities(provider TEXT NOT NULL,subject TEXT NOT NULL,user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,PRIMARY KEY(provider,subject),UNIQUE(provider,user_id));
CREATE TABLE IF NOT EXISTS authentication_settings(id INTEGER PRIMARY KEY CHECK(id=1),settings JSONB NOT NULL,encrypted_secrets TEXT NOT NULL DEFAULT '');

ALTER TABLE users ADD COLUMN IF NOT EXISTS auth_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE account_security ADD COLUMN IF NOT EXISTS pending_expires_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01';
CREATE INDEX IF NOT EXISTS api_charges_review_idx ON api_charges(created_at) WHERE outcome IN ('held','review');

ALTER TABLE authentication_settings ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS api_credentials_owner_idx ON api_credentials(owner_user_id,created_at DESC);
CREATE INDEX IF NOT EXISTS user_call_logs_retention_idx ON user_call_logs(created_at);
