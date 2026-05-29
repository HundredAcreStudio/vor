import { Link, useParams } from "react-router-dom";
import {
  fetchAttention,
  fetchDeadCode,
  fetchDecisions,
  fetchHealthSummary,
  fetchHotspots,
  fetchLanguages,
  fetchOverview,
  fetchRepo,
  type AttentionItem,
  type RepoSummary,
} from "../../api.ts";
import { Donut, HealthGauge } from "../../charts.tsx";
import { AsyncView, useAsync } from "../../useAsync.tsx";

export function RepoOverview() {
  const { repoId = "" } = useParams();
  const base = `/repositories/${repoId}`;
  const repo = useAsync(() => fetchRepo(repoId), [repoId]);
  const health = useAsync(() => fetchHealthSummary(repoId), [repoId]);
  const summary = useAsync(
    () => fetchOverview().then((o) => o.repos.find((r) => r.id === repoId)),
    [repoId],
  );
  const attention = useAsync(() => fetchAttention(repoId), [repoId]);
  const langs = useAsync(() => fetchLanguages(repoId), [repoId]);
  const hotspots = useAsync(() => fetchHotspots(repoId), [repoId]);
  const decisions = useAsync(() => fetchDecisions(repoId), [repoId]);
  const deadcode = useAsync(() => fetchDeadCode(repoId), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>Overview</h1>
      </header>

      {/* gauge + KPI cards */}
      <div className="ov-top">
        <AsyncView state={health}>
          {(h) => <HealthGauge score={h.averageScore} />}
        </AsyncView>
        <div className="kpi-row">
          <AsyncView state={health}>
            {(h) => <Kpi value={h.findingCount.toLocaleString()} label="findings" />}
          </AsyncView>
          <AsyncView state={summary}>
            {(s?: RepoSummary) => (
              <>
                <Kpi value={(s?.fileCount ?? 0).toLocaleString()} label="files" />
                <Kpi value={(s?.symbolCount ?? 0).toLocaleString()} label="symbols" />
              </>
            )}
          </AsyncView>
          <AsyncView state={langs}>
            {(l) => <Kpi value={String(l.languages.length)} label="languages" />}
          </AsyncView>
        </div>
      </div>

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

      {/* attention + languages */}
      <div className="ov-cols">
        <section className="panel attention-panel">
          <header className="panel-head">
            <h3>⚠ Attention needed</h3>
          </header>
          <AsyncView state={attention}>
            {(items) =>
              items.length === 0 ? (
                <p className="muted small">Nothing flagged — nice.</p>
              ) : (
                <ul className="attention-list">
                  {items.map((it, i) => (
                    <AttentionRow key={i} item={it} base={base} />
                  ))}
                </ul>
              )
            }
          </AsyncView>
        </section>

        <section className="panel">
          <header className="panel-head">
            <h3>Languages</h3>
          </header>
          <AsyncView state={langs}>
            {(l) =>
              l.languages.length === 0 ? (
                <p className="muted small">No language data.</p>
              ) : (
                <Donut slices={l.languages.slice(0, 8).map((s) => ({ label: s.language, value: s.files }))} />
              )
            }
          </AsyncView>
        </section>
      </div>

      {/* detail panels */}
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
                      <span className="muted">{h.commitCount90d} c/90d</span>
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
                  {safe > 0 && <span className="add small"> · {safe} safe to delete</span>}
                </p>
              );
            }}
          </AsyncView>
        </Panel>
      </div>
    </>
  );
}

const CATEGORY_META: Record<AttentionItem["category"], { label: string; cls: string }> = {
  knowledge_silo: { label: "Knowledge silo", cls: "att-silo" },
  ungoverned_hotspot: { label: "Hotspot", cls: "att-hotspot" },
  dead_code: { label: "Dead code", cls: "att-dead" },
  needs_review: { label: "Needs review", cls: "att-review" },
};

function AttentionRow({ item, base }: { item: AttentionItem; base: string }) {
  const meta = CATEGORY_META[item.category] ?? { label: item.category, cls: "" };
  return (
    <li className="attention-item">
      <Link to={`${base}/${item.link}`} className="attention-link">
        <span className="attention-title ellipsis">{item.title}</span>
        <span className={`att-tag ${meta.cls}`}>{meta.label}</span>
      </Link>
      <span className="muted small attention-detail">{item.detail}</span>
    </li>
  );
}

function Kpi({ value, label }: { value: string; label: string }) {
  return (
    <div className="kpi">
      <span className="kpi-value">{value}</span>
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
