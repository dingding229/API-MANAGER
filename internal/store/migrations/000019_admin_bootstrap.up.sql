CREATE TABLE admin_bootstrap(id INTEGER PRIMARY KEY CHECK(id=1),key_hash TEXT NOT NULL,consumed BOOLEAN NOT NULL DEFAULT FALSE);
INSERT INTO admin_bootstrap(id,key_hash,consumed) VALUES(1,'',EXISTS(SELECT 1 FROM users));
