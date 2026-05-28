-- +goose Up
-- +goose StatementBegin

-- Vector embeddings for semantic search. Mirrors the SQLite table; the
-- vector is a BYTEA float32 blob ranked application-side. A pgvector
-- column could replace this later without touching the Go API — the
-- vector package owns (de)serialisation.
CREATE TABLE IF NOT EXISTS embeddings (
    id             TEXT PRIMARY KEY,
    repository_id  TEXT NOT NULL,
    target_kind    TEXT NOT NULL,
    target_path    TEXT NOT NULL,
    model          TEXT NOT NULL,
    dim            INTEGER NOT NULL,
    vector         BYTEA NOT NULL,
    content_hash   TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
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
