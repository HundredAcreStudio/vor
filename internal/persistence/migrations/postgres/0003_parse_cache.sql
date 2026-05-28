-- +goose Up
-- +goose StatementBegin

-- Per-file parse cache for incremental indexing. See the SQLite copy for
-- the full rationale.
CREATE TABLE IF NOT EXISTS parse_cache (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    file_path      TEXT NOT NULL,
    content_hash   TEXT NOT NULL,
    parser_version TEXT NOT NULL DEFAULT '',
    parsed_json    TEXT NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_parse_cache ON parse_cache(repository_id, file_path);
CREATE INDEX IF NOT EXISTS ix_parse_cache_repo ON parse_cache(repository_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS parse_cache;
-- +goose StatementEnd
