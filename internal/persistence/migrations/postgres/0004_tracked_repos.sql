-- +goose Up
-- Daemon registry columns. `vor serve` watches repositories where
-- tracked = 1; register/unregister flips this at runtime. `ephemeral`
-- marks disposable repos (e.g. agent worktrees) whose indexed data is
-- purged on unregister, distinguishing them from durable tracked repos.
ALTER TABLE repositories ADD COLUMN IF NOT EXISTS tracked INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repositories ADD COLUMN IF NOT EXISTS ephemeral INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repositories ADD COLUMN IF NOT EXISTS tracked_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS ix_repositories_tracked ON repositories(tracked);

-- +goose Down
DROP INDEX IF EXISTS ix_repositories_tracked;
ALTER TABLE repositories DROP COLUMN IF EXISTS tracked_at;
ALTER TABLE repositories DROP COLUMN IF EXISTS ephemeral;
ALTER TABLE repositories DROP COLUMN IF EXISTS tracked;
