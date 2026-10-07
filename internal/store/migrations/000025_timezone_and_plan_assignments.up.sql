ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS time_zone TEXT NOT NULL DEFAULT 'Asia/Shanghai';
UPDATE site_settings SET settings=jsonb_set(settings,'{site,time_zone}','"Asia/Shanghai"'),version=version+1 WHERE COALESCE(settings->'site'->>'time_zone','')='';
