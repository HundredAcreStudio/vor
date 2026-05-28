-- +goose Up
-- +goose StatementBegin

-- Vector embeddings for semantic search. One row per embedded target
-- (a wiki page or a symbol). The vector is stored as a raw little-
-- endian float32 BLOB; cosine similarity is computed application-side
-- in internal/persistence/vector (SQLite has no native vector type).
-- Repo-scale corpora (hundreds–thousands of rows) rank fine in Go.
CREATE TABLE IF NOT EXISTS embeddings (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL,
    target_kind    TEXT NOT NULL,          -- 'page' | 'symbol'
    target_path    TEXT NOT NULL,          -- page target_path or symbol node_id
    model          TEXT NOT NULL,
    dim            INTEGER NOT NULL,
    vector         BLOB NOT NULL,          -- dim * 4 bytes, little-endian float32
    content_hash   TEXT NOT NULL DEFAULT '', -- source hash so re-embeds are skippable
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_embeddings_target
    ON embeddings(repository_id, target_kind, target_path);
CREATE INDEX IF NOT EXISTS ix_embeddings_repo ON embeddings(repository_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS embeddings;
-- +goose StatementEnd
