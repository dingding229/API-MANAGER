-- Match verification replacement/consumption and session listing predicates.
CREATE INDEX IF NOT EXISTS account_verifications_purpose_subject_binding_idx ON account_verifications(purpose,subject,binding);
CREATE INDEX IF NOT EXISTS user_sessions_user_activity_idx ON user_sessions(user_id,last_seen_at DESC,session_id);
-- Preserve a stable ordering for global call-log pages and card-use lookups.
CREATE INDEX IF NOT EXISTS user_call_logs_created_id_idx ON user_call_logs(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS redeem_cards_user_used_idx ON redeem_cards(redeemed_by,redeemed_at DESC) WHERE redeemed_by IS NOT NULL;
INSERT INTO permissions(id,code,description) VALUES(gen_random_uuid(),'database.manage','查看、备份和恢复数据库') ON CONFLICT(code) DO NOTHING;
