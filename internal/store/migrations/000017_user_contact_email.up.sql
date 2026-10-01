-- Preserve every existing login name and password; introduce a separate contact email.
ALTER TABLE users RENAME COLUMN email TO username;
ALTER TABLE users ADD COLUMN email TEXT;
UPDATE users SET email=lower(username)
WHERE username ~ '^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$'
AND NOT EXISTS (SELECT 1 FROM users other WHERE other.id <> users.id AND lower(other.username)=lower(users.username));
CREATE UNIQUE INDEX users_contact_email_unique ON users(lower(email)) WHERE email IS NOT NULL;

CREATE TABLE password_resets (
    key_hash TEXT PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    username_snapshot TEXT NOT NULL,
    email_snapshot TEXT NOT NULL,
    password_hash_snapshot TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX password_resets_user_unique ON password_resets(user_id);
