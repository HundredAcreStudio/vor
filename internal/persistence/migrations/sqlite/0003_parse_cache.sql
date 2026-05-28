-- +goose Up
-- +goose StatementBegin

-- Per-file parse cache for incremental indexing. A row stores the parsed
-- output (symbols/imports/calls/heritage, JSON-encoded) for one file at a
-- given content hash. On re-index, files whose hash + parser_version match
-- skip the expensive tree-sitter parse and reuse the cached result.
CREATE TABLE IF NOT EXISTS parse_cache (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL,
    file_path      TEXT NOT NULL,
    content_hash   TEXT NOT NULL,
    parser_version TEXT NOT NULL DEFAULT '',
    parsed_json    TEXT NOT NULL,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_parse_cache ON parse_cache(repository_id, file_path);
CREATE INDEX IF NOT EXISTS ix_parse_cache_repo ON parse_cache(repository_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS parse_cache;
-- +goose StatementEnd
