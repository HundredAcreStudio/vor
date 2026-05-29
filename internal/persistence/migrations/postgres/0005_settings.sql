-- +goose Up
-- Configuration moved out of YAML files into the database. A row with
-- repository_id = '' is a global default; a row with a real repository_id
-- overrides it for that repo. Values are JSON-encoded so non-string settings
-- (ints, bools, lists, and structured values like health_rules / watch)
-- round-trip through one column.
CREATE TABLE IF NOT EXISTS settings (
    repository_id TEXT        NOT NULL DEFAULT '',
    key           TEXT        NOT NULL,
    value         TEXT        NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, key)
);

-- +goose Down
DROP TABLE IF EXISTS settings;
