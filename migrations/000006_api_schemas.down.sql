ALTER TABLE apis
    DROP COLUMN IF EXISTS response_schema,
    DROP COLUMN IF EXISTS request_schema;
