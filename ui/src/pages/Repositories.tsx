import { useState } from "react";
import { Link } from "react-router-dom";
import { fetchOverview, registerRepo, type RepoSummary } from "../api.ts";
import { AsyncView, useAsync } from "../useAsync.tsx";

/** Repositories renders the landing page listing all indexed repositories as
 * cards, with an add-repository form that reloads the list on success. */
export function Repositories() {
  const [nonce, setNonce] = useState(0);
  const state = useAsync(() => fetchOverview().then((o) => o.repos), [nonce]);
  const reload = () => setNonce((n) => n + 1);

  return (
    <main className="content">
      <header className="page-header">
        <h1>
          Repositories
          {state.status === "ready" && <span className="count">{state.data.length}</span>}
        </h1>
      </header>

      <AddRepo onAdded={reload} />

      <AsyncView state={state}>
        {(repos) =>
          repos.length === 0 ? (
            <p className="muted">No repositories indexed yet. Add one above.</p>
          ) : (
            <div className="repo-grid">
              {repos.map((r) => (
                <RepoCard key={r.id} repo={r} />
              ))}
            </div>
          )
        }
      </AsyncView>
    </main>
  );
}

/** AddRepo renders the form for registering a repository by absolute path,
 * showing busy state and any registration error; calls onAdded on success. */
function AddRepo({ onAdded }: { onAdded: () => void }) {
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  /** submit registers the trimmed path, clearing the field and notifying the
   * parent on success, or surfacing the error otherwise. */
  function submit(e: React.FormEvent) {
    e.preventDefault();
    const p = path.trim();
    if (!p) return;
    setBusy(true);
    setError(null);
    registerRepo(p)
      .then(() => {
        setPath("");
        onAdded();
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false));
  }

  return (
    <form className="add-repo" onSubmit={submit}>
      <input
        className="wiki-search"
        placeholder="Absolute path to a repository…  (e.g. /Users/me/projects/app)"
        value={path}
        onChange={(e) => setPath(e.target.value)}
      />
      <button className="btn" type="submit" disabled={busy}>
        {busy ? "Adding…" : "Add repository"}
      </button>
      {error && <span className="add-repo-error error">{error}</span>}
    </form>
  );
}

/** RepoCard renders a clickable summary card for one repository: name, health
 * badge, file/symbol/finding counts, head commit, and last-indexed time. */
function RepoCard({ repo }: { repo: RepoSummary }) {
  return (
    <Link to={`/repositories/${repo.slug}/overview`} className="repo-card">
      <div className="repo-card-head">
        <h3 title={repo.localPath}>{repo.name}</h3>
        <HealthBadge score={repo.healthAvg} />
      </div>
      <dl className="stats">
        <Stat label="Files" value={fmt(repo.fileCount)} />
        <Stat label="Symbols" value={fmt(repo.symbolCount)} />
        <Stat label="Findings" value={fmt(repo.findingCount)} />
      </dl>
      <footer className="repo-card-foot muted">
        {repo.headCommit && <code>{repo.headCommit.slice(0, 10)}</code>}
        <span>{relativeTime(repo.lastIndexed)}</span>
      </footer>
    </Link>
  );
}

/** Stat renders a single labeled statistic as a <dt>/<dd> pair. */
function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="stat">
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

/** HealthBadge renders a colored badge for a 1–10 health score, tiered into
 * good/warn/bad. */
function HealthBadge({ score }: { score: number }) {
  // Health is a 1–10 score (10 = perfect).
  const tier = score >= 7.5 ? "good" : score >= 5 ? "warn" : "bad";
  return (
    <span className={`health-badge health-${tier}`} title="average health score (1–10)">
      {Number.isFinite(score) ? score.toFixed(1) : "—"}
    </span>
  );
}

function fmt(n: number): string {
  return n.toLocaleString();
}

/** relativeTime formats an ISO timestamp as a coarse "just now"/"Nm/h/d ago"
 * string, returning "never indexed" for an empty input. */
function relativeTime(iso: string): string {
  if (!iso) return "never indexed";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return iso;
  const secs = Math.round((Date.now() - then) / 1000);
  if (secs < 60) return "just now";
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  return `${days}d ago`;
}
