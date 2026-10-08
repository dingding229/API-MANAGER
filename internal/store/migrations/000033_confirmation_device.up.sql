ALTER TABLE users ADD COLUMN confirmation_method TEXT NOT NULL DEFAULT 'password' CHECK (confirmation_method IN ('password','passkey'));
ALTER TABLE user_sessions ADD COLUMN device_binding_hash TEXT NOT NULL DEFAULT '';
