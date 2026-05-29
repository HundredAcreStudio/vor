import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { fetchOverview, type RepoSummary } from "../api.ts";

type LoadState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; repos: RepoSummary[] };

export function Repositories() {
  const [state, setState] = useState<LoadState>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    fetchOverview()
      .then((o) => {
        if (!cancelled) setState({ status: "ready", repos: o.repos });
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setState({ status: "error", message: err instanceof Error ? err.message : String(err) });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <main className="content">
      <header className="page-header">
        <h1>
          Repositories
          {state.status === "ready" && <span className="count">{state.repos.length}</span>}
        </h1>
      </header>
      {state.status === "loading" && <p className="muted">Loading…</p>}
      {state.status === "error" && <p className="error">Failed to load: {state.message}</p>}
      {state.status === "ready" &&
        (state.repos.length === 0 ? (
          <p className="muted">
            No repositories indexed yet. Run <code>vor ingest</code> to index one.
          </p>
        ) : (
          <RepoGrid repos={state.repos} />
        ))}
    </main>
  );
}

function RepoGrid({ repos }: { repos: RepoSummary[] }) {
  return (
    <div className="repo-grid">
      {repos.map((r) => (
        <RepoCard key={r.id} repo={r} />
      ))}
    </div>
  );
}

function RepoCard({ repo }: { repo: RepoSummary }) {
  return (
    <Link to={`/repositories/${repo.id}/overview`} className="repo-card">
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

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="stat">
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

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
