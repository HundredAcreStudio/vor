-- +goose Up
-- Repo-level commit category tally (feature/fix/refactor/docs/dependency/...),
-- produced by the git phase classifying each non-merge commit subject.
CREATE TABLE IF NOT EXISTS commit_categories (
    repository_id TEXT    NOT NULL,
    category      TEXT    NOT NULL,
    count         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (repository_id, category)
);

-- +goose Down
DROP TABLE IF EXISTS commit_categories;
