ALTER TABLE apis
    DROP COLUMN IF EXISTS circuit_breaker_reset_seconds,
    DROP COLUMN IF EXISTS circuit_breaker_threshold,
    DROP COLUMN IF EXISTS upstream_retries,
    DROP COLUMN IF EXISTS upstream_timeout_ms,
    DROP COLUMN IF EXISTS parameters_schema;
