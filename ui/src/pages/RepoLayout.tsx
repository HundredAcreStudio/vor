import { Link, NavLink, Outlet, useLocation, useParams } from "react-router-dom";
import { fetchRepo } from "../api.ts";
import { useAsync } from "../useAsync.tsx";

// REPO_NAV is the repo-scoped menu. `soon` marks sections without a backend.
const REPO_NAV: { to: string; label: string; soon?: boolean }[] = [
  { to: "overview", label: "Overview" },
  { to: "wiki", label: "Wiki" },
  { to: "search", label: "Search" },
  { to: "risk", label: "Risk" },
  { to: "hotspots", label: "Hotspots" },
  { to: "dead-code", label: "Dead code" },
  { to: "graph", label: "Graph" },
  { to: "symbols", label: "Symbols" },
  { to: "dependencies", label: "Dependencies", soon: true },
  { to: "decisions", label: "Decisions" },
  { to: "contributors", label: "Contributors", soon: true },
  { to: "coverage", label: "Coverage", soon: true },
  { to: "costs", label: "Costs", soon: true },
  { to: "security", label: "Security", soon: true },
  { to: "activity", label: "Activity", soon: true },
];

export function RepoLayout() {
  const { repoId = "" } = useParams();
  const location = useLocation();
  const repo = useAsync(() => fetchRepo(repoId), [repoId]);
  const name = repo.status === "ready" ? repo.data.name : repoId.slice(0, 8);
  const branch = repo.status === "ready" ? repo.data.defaultBranch : "—";

  const segment = location.pathname.split("/").pop() ?? "";
  const sectionLabel = REPO_NAV.find((n) => n.to === segment)?.label ?? "Overview";

  return (
    <>
      <aside className="sidebar">
        <Link to="/repositories" className="back-link">
          ← My Repos
        </Link>
        <div className="repo-section-label">Repository</div>
        <Link
          to="/repositories"
          className="selector"
          title={repo.status === "ready" ? repo.data.localPath : repoId}
        >
          <span className="dot" />
          <span className="selector-text">{name}</span>
          <span className="selector-caret">▾</span>
        </Link>
        <div className="selector selector-static" title="branch switching coming soon">
          <span className="selector-icon"></span>
          <span className="selector-text">{branch}</span>
        </div>
        <nav className="nav">
          {REPO_NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) => "nav-link" + (isActive ? " nav-link-active" : "")}
            >
              {item.label}
              {item.soon && <span className="soon-tag">soon</span>}
            </NavLink>
          ))}
        </nav>
      </aside>
      <div className="repo-main">
        <header className="breadcrumb">
          <Link to="/repositories" className="crumb">
            Repo
          </Link>
          <span className="crumb-sep">/</span>
          <span className="crumb mono">{repoId.slice(0, 8)}</span>
          <span className="crumb-sep">/</span>
          <span className="crumb crumb-current">{sectionLabel}</span>
        </header>
        <main className="content">
          <Outlet />
        </main>
      </div>
    </>
  );
}
