CREATE TABLE site_settings (
 id SMALLINT PRIMARY KEY CHECK(id=1),
 version BIGINT NOT NULL CHECK(version>0),
 settings JSONB NOT NULL,
 encrypted_smtp_password TEXT NOT NULL DEFAULT '',
 updated_at TIMESTAMPTZ NOT NULL
);
