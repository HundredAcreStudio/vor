-- +goose Up
-- Tag each health snapshot with the commit + branch it was taken at, so the
-- snapshot history is a per-commit timeline (one snapshot per distinct commit)
-- and supports branch-scoped trends/diffs.
ALTER TABLE health_snapshots ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE health_snapshots ADD COLUMN branch TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE health_snapshots DROP COLUMN commit_sha;
ALTER TABLE health_snapshots DROP COLUMN branch;
