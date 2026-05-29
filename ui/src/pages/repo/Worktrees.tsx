export function RepoWorktrees() {
  return (
    <>
      <header className="page-header">
        <h1>Worktrees</h1>
      </header>
      <div className="placeholder">
        <p>
          This panel will list the git worktrees configured for this repository
          and their state — branch, last sync, indexing status, and any running
          agents.
        </p>
        <p className="muted">No backend yet — coming soon.</p>
      </div>
    </>
  );
}
