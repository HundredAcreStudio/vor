-- +goose Up
-- Repo-level commit category tally (feature/fix/refactor/docs/dependency/...),
-- produced by the git phase classifying each non-merge commit subject. Powers
-- the overview's Commit Categories donut + Commit Activity bar.
CREATE TABLE commit_categories (
    repository_id TEXT    NOT NULL,
    category      TEXT    NOT NULL,
    count         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (repository_id, category)
);

-- +goose Down
DROP TABLE commit_categories;
