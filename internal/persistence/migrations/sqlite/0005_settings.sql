-- +goose Up
-- Configuration moved out of YAML files into the database (see `vor` config
-- on the dashboard). A row with repository_id = '' is a global default; a row
-- with a real repository_id overrides it for that repo. Values are
-- JSON-encoded so non-string settings (ints, bools, lists, and structured
-- values like health_rules / watch) round-trip through one column.
--
-- '' is used for the global scope rather than NULL because SQLite treats NULL
-- as distinct in PRIMARY KEY / UNIQUE, which would let duplicate global rows
-- for the same key slip in.
CREATE TABLE settings (
    repository_id TEXT     NOT NULL DEFAULT '',
    key           TEXT     NOT NULL,
    value         TEXT     NOT NULL,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (repository_id, key)
);

-- +goose Down
DROP TABLE settings;
