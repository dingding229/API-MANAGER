-- Empty means follow the website's display preference. Billing windows keep their existing timezone.
ALTER TABLE users ADD COLUMN time_zone TEXT NOT NULL DEFAULT '';
