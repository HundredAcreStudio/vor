import { Link, useParams } from "react-router-dom";
import {
  fetchAttention,
  fetchCommunities,
  fetchDeadCode,
  fetchDecisions,
  fetchDependencyMatrix,
  fetchEntryPoints,
  fetchExecutionFlows,
  fetchGitInsights,
  fetchHealthSummary,
  fetchHotspots,
  fetchLanguages,
  fetchOverview,
  fetchRepo,
  type AttentionItem,
  type FlowNode,
  type RepoSummary,
} from "../../api.ts";
import { Donut, Heatmap, HealthGauge } from "../../charts.tsx";
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
  const communities = useAsync(() => fetchCommunities(repoId), [repoId]);
  const entryPoints = useAsync(() => fetchEntryPoints(repoId), [repoId]);
  const gitInsights = useAsync(() => fetchGitInsights(repoId), [repoId]);
  const flows = useAsync(() => fetchExecutionFlows(repoId), [repoId]);
  const matrix = useAsync(() => fetchDependencyMatrix(repoId), [repoId]);

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

      {/* git insights */}
      <h2 className="section-title">Git insights</h2>
      <div className="panel-grid">
        <Panel title="Churn distribution" to={`${base}/hotspots`}>
          <AsyncView state={gitInsights}>
            {(g) => (
              <Bars data={g.churnBuckets.map((b) => ({ label: b.label, value: b.count }))} graded />
            )}
          </AsyncView>
        </Panel>
        <Panel title="Bus factor" to={`${base}/hotspots`}>
          <AsyncView state={gitInsights}>
            {(g) => (
              <>
                <div className="busfactor-legend">
                  <span>
                    <span className="dot good" /> Safe ≥3 <b>{g.busFactor.safe}</b>
                  </span>
                  <span>
                    <span className="dot warn" /> Warning 2 <b>{g.busFactor.warning}</b>
                  </span>
                  <span>
                    <span className="dot bad" /> Risk ≤1 <b>{g.busFactor.risk}</b>
                  </span>
                </div>
                <div className="busfactor-track">
                  <span className="seg good" style={segWidth(g.busFactor.safe, g.busFactor.total)} />
                  <span className="seg warn" style={segWidth(g.busFactor.warning, g.busFactor.total)} />
                  <span className="seg bad" style={segWidth(g.busFactor.risk, g.busFactor.total)} />
                </div>
                <ul className="mini-list" style={{ marginTop: 10 }}>
                  {g.busFactor.riskFiles.map((f) => (
                    <li key={f.path}>
                      <span className="mono path" title={f.path}>
                        {f.path}
                      </span>
                      <span className="muted">
                        {f.contributors} contributor{f.contributors === 1 ? "" : "s"}
                      </span>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </AsyncView>
        </Panel>
        <Panel title="Top contributors" to={`${base}/hotspots`}>
          <AsyncView state={gitInsights}>
            {(g) =>
              g.contributors.length === 0 ? (
                <p className="muted small">No contributor data.</p>
              ) : (
                <ul className="mini-list">
                  {g.contributors.map((c) => (
                    <li key={c.name}>
                      <span className="ellipsis">{c.name}</span>
                      <span className="muted">{c.commits.toLocaleString()}</span>
                    </li>
                  ))}
                </ul>
              )
            }
          </AsyncView>
        </Panel>
      </div>

      {/* dependency heatmap */}
      <h2 className="section-title">Dependency heatmap</h2>
      <section className="panel">
        <p className="muted small" style={{ marginTop: 0 }}>
          Module-to-module import density — brighter cells mean more dependencies.
        </p>
        <AsyncView state={matrix}>
          {(m) =>
            m.modules.length === 0 ? (
              <p className="muted small">No import edges.</p>
            ) : (
              <Heatmap modules={m.modules} cells={m.cells} />
            )
          }
        </AsyncView>
      </section>

      {/* structure */}
      <h2 className="section-title">Structure</h2>
      <div className="panel-grid">
        <Panel title="Architecture communities" to={`${base}/graph`}>
          <AsyncView state={communities}>
            {(cs) =>
              cs.length === 0 ? (
                <p className="muted small">No communities detected.</p>
              ) : (
                <ul className="mini-list">
                  {cs.slice(0, 8).map((c) => (
                    <li key={c.communityId}>
                      <span className="ellipsis">{c.label}</span>
                      <span className="muted">{c.size} files</span>
                    </li>
                  ))}
                </ul>
              )
            }
          </AsyncView>
        </Panel>
        <Panel title="Entry points" to={`${base}/graph`}>
          <AsyncView state={entryPoints}>
            {(eps) =>
              eps.length === 0 ? (
                <p className="muted small">None indexed.</p>
              ) : (
                <ul className="mini-list">
                  {eps.slice(0, 8).map((e) => (
                    <li key={e.path}>
                      <span className="mono path" title={e.path}>
                        {e.path}
                      </span>
                      {e.language && <span className="muted">{e.language}</span>}
                    </li>
                  ))}
                </ul>
              )
            }
          </AsyncView>
        </Panel>
        <Panel title="Execution flows" to={`${base}/graph`}>
          <AsyncView state={flows}>
            {(fs) =>
              fs.length === 0 ? (
                <p className="muted small">No entry points to trace.</p>
              ) : (
                <div className="flow-tree">
                  {fs.slice(0, 3).map((f, i) => (
                    <FlowBranch key={i} node={f} depth={0} />
                  ))}
                </div>
              )
            }
          </AsyncView>
        </Panel>
      </div>
    </>
  );
}

// gradeColor maps a 0..1 position to a green→yellow→orange→red hue, used to
// color the churn-distribution bars (higher churn = hotter).
function gradeColor(t: number): string {
  if (t < 0.25) return "var(--good)";
  if (t < 0.5) return "#b5b324";
  if (t < 0.75) return "var(--warn)";
  return "var(--bad)";
}

function Bars({ data, graded }: { data: { label: string; value: number }[]; graded?: boolean }) {
  const max = Math.max(1, ...data.map((d) => d.value));
  return (
    <div className="bars">
      {data.map((d, i) => (
        <div className="bar-row" key={d.label}>
          <span className="bar-label muted">{d.label}</span>
          <span className="bar-track">
            <span
              className="bar-fill"
              style={{
                width: `${(d.value / max) * 100}%`,
                background: graded ? gradeColor(i / Math.max(1, data.length - 1)) : undefined,
              }}
            />
          </span>
          <span className="bar-value">{d.value}</span>
        </div>
      ))}
    </div>
  );
}

function segWidth(value: number, total: number): React.CSSProperties {
  return { width: `${total > 0 ? (value / total) * 100 : 0}%` };
}

function FlowBranch({ node, depth }: { node: FlowNode; depth: number }) {
  const short = node.node.split("/").pop() ?? node.node;
  return (
    <div className="flow-branch" style={{ paddingLeft: depth * 12 }}>
      <span className="mono" title={node.node}>
        {depth > 0 ? "└ " : ""}
        {short}
      </span>
      {(node.children ?? []).slice(0, 4).map((c, i) => (
        <FlowBranch key={i} node={c} depth={depth + 1} />
      ))}
    </div>
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
