CREATE TABLE IF NOT EXISTS plugin_data (
    plugin_name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    data_key TEXT NOT NULL,
    data_value JSONB NOT NULL DEFAULT '{}'::JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_name, namespace, data_key)
);
