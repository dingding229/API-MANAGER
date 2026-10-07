INSERT INTO permissions(id,code,description) VALUES(gen_random_uuid(),'user.security.reset','重置用户双重验证与恢复码') ON CONFLICT(code) DO NOTHING;
