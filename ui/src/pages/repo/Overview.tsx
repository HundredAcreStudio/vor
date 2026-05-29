import { Link, useParams } from "react-router-dom";
import {
  fetchDeadCode,
  fetchDecisions,
  fetchHealthSummary,
  fetchHotspots,
  fetchOverview,
  fetchRepo,
  type RepoSummary,
} from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";

export function RepoOverview() {
  const { repoId = "" } = useParams();
  const repo = useAsync(() => fetchRepo(repoId), [repoId]);
  const health = useAsync(() => fetchHealthSummary(repoId), [repoId]);
  const summary = useAsync(
    () => fetchOverview().then((o) => o.repos.find((r) => r.id === repoId)),
    [repoId],
  );
  const hotspots = useAsync(() => fetchHotspots(repoId), [repoId]);
  const decisions = useAsync(() => fetchDecisions(repoId), [repoId]);
  const deadcode = useAsync(() => fetchDeadCode(repoId), [repoId]);

  const base = `/repositories/${repoId}`;

  return (
    <>
      <header className="page-header">
        <h1>Overview</h1>
      </header>

      <AsyncView state={health}>
        {(h) => {
          const tier = h.averageScore >= 7.5 ? "good" : h.averageScore >= 5 ? "warn" : "bad";
          return (
            <div className="kpi-row">
              <Kpi value={h.averageScore.toFixed(1)} label="health (1–10)" tier={tier} />
              <Kpi value={h.findingCount.toLocaleString()} label="findings" />
              <AsyncView state={summary}>
                {(s?: RepoSummary) => (
                  <>
                    <Kpi value={(s?.fileCount ?? 0).toLocaleString()} label="files" />
                    <Kpi value={(s?.symbolCount ?? 0).toLocaleString()} label="symbols" />
                  </>
                )}
              </AsyncView>
            </div>
          );
        }}
      </AsyncView>

      <AsyncView state={repo}>
        {(r) => (
          <dl className="facts">
            <Fact label="Local path" value={r.localPath} mono />
            <Fact label="Default branch" value={r.defaultBranch} mono />
            <Fact label="Head commit" value={r.headCommit ? r.headCommit.slice(0, 12) : "—"} mono />
            <Fact label="Last indexed" value={new Date(r.updatedAt).toLocaleString()} />
          </dl>
        )}
      </AsyncView>

      <div className="panel-grid">
        <Panel title="Health breakdown" to={`${base}/risk`}>
          <AsyncView state={health}>
            {(h) => {
              const rows = Object.entries(h.findingsByBiomarker).sort((a, b) => b[1] - a[1]);
              return rows.length === 0 ? (
                <p className="muted small">No findings.</p>
              ) : (
                <div className="chips">
                  {rows.map(([k, n]) => (
                    <span className="chip" key={k}>
                      {k.replace(/_/g, " ")}
                      <b>{n}</b>
                    </span>
                  ))}
                </div>
              );
            }}
          </AsyncView>
        </Panel>

        <Panel title="Top hotspots" to={`${base}/hotspots`}>
          <AsyncView state={hotspots}>
            {(rows) =>
              rows.length === 0 ? (
                <p className="muted small">No hotspots.</p>
              ) : (
                <ul className="mini-list">
                  {rows.slice(0, 6).map((h, i) => (
                    <li key={i}>
                      <span className="mono path" title={h.path}>
                        {h.path}
                      </span>
                      <span className="muted">{h.commitCount90d} commits/90d</span>
                    </li>
                  ))}
                </ul>
              )
            }
          </AsyncView>
        </Panel>

        <Panel title="Recent decisions" to={`${base}/decisions`}>
          <AsyncView state={decisions}>
            {(rows) =>
              rows.length === 0 ? (
                <p className="muted small">No decisions detected.</p>
              ) : (
                <ul className="mini-list">
                  {rows.slice(0, 5).map((d, i) => (
                    <li key={i}>
                      <span className="ellipsis">{d.decision || d.title}</span>
                      <span className="badge">{d.source.replace(/_/g, " ")}</span>
                    </li>
                  ))}
                </ul>
              )
            }
          </AsyncView>
        </Panel>

        <Panel title="Dead code" to={`${base}/dead-code`}>
          <AsyncView state={deadcode}>
            {(rows) => {
              const safe = rows.filter((d) => d.safeToDelete).length;
              return rows.length === 0 ? (
                <p className="muted small">None detected.</p>
              ) : (
                <p className="big-stat">
                  {rows.length} <span className="muted small">findings</span>
                  {safe > 0 && (
                    <span className="add small">
                      {" "}
                      · {safe} safe to delete
                    </span>
                  )}
                </p>
              );
            }}
          </AsyncView>
        </Panel>
      </div>
    </>
  );
}

function Kpi({ value, label, tier }: { value: string; label: string; tier?: string }) {
  return (
    <div className="kpi">
      <span className={`kpi-value${tier ? ` health-${tier}` : ""}`}>{value}</span>
      <span className="kpi-label">{label}</span>
    </div>
  );
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="fact">
      <dt>{label}</dt>
      <dd className={mono ? "mono" : undefined}>{value}</dd>
    </div>
  );
}

function Panel({ title, to, children }: { title: string; to: string; children: React.ReactNode }) {
  return (
    <section className="panel">
      <header className="panel-head">
        <h3>{title}</h3>
        <Link to={to} className="panel-link">
          view all →
        </Link>
      </header>
      {children}
    </section>
  );
}
