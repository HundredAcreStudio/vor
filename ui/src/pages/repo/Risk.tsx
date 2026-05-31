import { useState } from "react";
import { useParams } from "react-router-dom";
import {
  fetchDeadCode,
  fetchDependencyMatrix,
  fetchHotspots,
  fetchModules,
  fetchRisk,
  fetchRiskContributors,
  fetchRiskOwnership,
  fetchSecurity,
  type RiskData,
} from "../../api.ts";
import { Donut, Heatmap } from "../../charts.tsx";
import { Icon } from "../../Icon.tsx";
import { ContributorNetwork, Treemap } from "../../RiskCharts.tsx";
import { AsyncView, useAsync } from "../../useAsync.tsx";

// RepoRisk surfaces where risk is concentrated across the repo: ownership
// silos, churn hotspots, dead code, stale decisions and security findings.
// The header + stat cards stay fixed; a tab bar swaps the analysis below.
type Tab = "heatmap" | "hotspots" | "modules" | "deadcode" | "impact" | "security";

const TABS: { id: Tab; label: string }[] = [
  { id: "heatmap", label: "Heatmap" },
  { id: "hotspots", label: "Hotspots" },
  { id: "modules", label: "Modules" },
  { id: "deadcode", label: "Dead Code" },
  { id: "impact", label: "Impact" },
  { id: "security", label: "Security" },
];

export function RepoRisk() {
  const { repoId = "" } = useParams();
  const [tab, setTab] = useState<Tab>("heatmap");
  const risk = useAsync(() => fetchRisk(repoId), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>
          <Icon name="shield" size={22} /> Risk
        </h1>
        <p className="muted page-sub">
          Where risk is concentrated — ownership silos, churn hotspots, dead code, and stale
          decisions.
        </p>
      </header>

      <AsyncView state={risk}>{(d) => <StatCards data={d} />}</AsyncView>

      <nav className="risk-tabs" role="tablist">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={tab === t.id}
            className={"chip-btn" + (tab === t.id ? " chip-btn-on" : "")}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>

      <div className="risk-tab-body">
        {tab === "heatmap" && <HeatmapTab repoId={repoId} risk={risk} />}
        {tab === "hotspots" && <HotspotsTab repoId={repoId} />}
        {tab === "modules" && <ModulesTab repoId={repoId} />}
        {tab === "deadcode" && <DeadCodeTab repoId={repoId} />}
        {tab === "impact" && <ImpactTab repoId={repoId} />}
        {tab === "security" && <SecurityTab repoId={repoId} />}
      </div>
    </>
  );
}

/** Renders the row of summary stat cards (hotspots, silos, dead code, stale decisions, security) at the top of the Risk page. */
function StatCards({ data }: { data: RiskData }) {
  const c = data.counts;
  return (
    <div className="stat-cards">
      <StatCard label="Hotspots" value={c.hotspots} sub="high-churn files" icon="local_fire_department" />
      <StatCard label="Silos" value={c.silos} sub="bus factor ≤ 1" icon="group" />
      <StatCard label="Dead code" value={c.deadCode} sub="findings" icon="delete" />
      <StatCard label="Stale decisions" value={c.staleDecisions} sub="need review" icon="lightbulb" />
      <StatCard label="Security" value={c.securityHigh} sub="high findings" icon="shield" />
    </div>
  );
}

// ---- Heatmap tab: ownership treemap + bus factor + contributors + network --

/** Heatmap tab: ownership treemap (toggleable module/file mode), bus-factor breakdown, and the contributor co-authorship network. */
function HeatmapTab({
  repoId,
  risk,
}: {
  repoId: string;
  risk: ReturnType<typeof useAsync<RiskData>>;
}) {
  const [mode, setMode] = useState<"module" | "file">("module");
  const ownership = useAsync(() => fetchRiskOwnership(repoId, mode), [repoId, mode]);
  const contributors = useAsync(() => fetchRiskContributors(repoId), [repoId]);

  return (
    <>
      <section className="panel">
        <header className="panel-head">
          <h3>Ownership Map</h3>
          <div className="chip-row">
            <button
              type="button"
              className={"chip-btn" + (mode === "module" ? " chip-btn-on" : "")}
              onClick={() => setMode("module")}
            >
              Module
            </button>
            <button
              type="button"
              className={"chip-btn" + (mode === "file" ? " chip-btn-on" : "")}
              onClick={() => setMode("file")}
            >
              File
            </button>
          </div>
        </header>
        <AsyncView state={ownership}>{(o) => <Treemap cells={o.cells} />}</AsyncView>
      </section>

      <AsyncView state={risk}>{(d) => <BusFactorRow data={d} />}</AsyncView>

      <section className="panel risk-network-panel">
        <header className="panel-head">
          <h3>Contributor Network</h3>
          <span className="muted small">co-authorship — edge width = shared files</span>
        </header>
        <AsyncView state={contributors}>
          {(c) => <ContributorNetwork nodes={c.nodes} edges={c.edges} />}
        </AsyncView>
      </section>
    </>
  );
}

/** Side-by-side panels: a bus-factor donut with the highest-risk files, and a bar list of top contributors by commit count. */
function BusFactorRow({ data }: { data: RiskData }) {
  const bf = data.busFactor;
  const contributors = [...data.topContributors].sort((a, b) => b.commits - a.commits);
  const maxCommits = Math.max(1, ...contributors.map((x) => x.commits));

  return (
    <div className="risk-cols">
      <section className="panel">
        <header className="panel-head">
          <h3>Bus Factor Analysis</h3>
          <span className="muted small">{bf.total.toLocaleString()} files</span>
        </header>
        {bf.total === 0 ? (
          <p className="muted small">No ownership data.</p>
        ) : (
          <>
            <Donut
              slices={[
                { label: `Safe (≥3) — ${bf.safe}`, value: bf.safe },
                { label: `Warning (2) — ${bf.warning}`, value: bf.warning },
                { label: `Risk (≤1) — ${bf.risk}`, value: bf.risk },
              ]}
              colors={["var(--good)", "var(--warn)", "var(--bad)"]}
            />
            {bf.riskFiles.length > 0 && (
              <>
                <h4 className="risk-subhead">Highest risk files</h4>
                <ul className="mini-list">
                  {bf.riskFiles.slice(0, 8).map((f) => (
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
          </>
        )}
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>Top Contributors</h3>
        </header>
        {contributors.length === 0 ? (
          <p className="muted small">No contributor data.</p>
        ) : (
          <ul className="contrib-list">
            {contributors.map((p) => (
              <li key={p.name} className="contrib-row">
                <span className="contrib-name ellipsis" title={p.name}>
                  {p.name}
                </span>
                <span className="contrib-track">
                  <span
                    className="contrib-fill"
                    style={{ width: `${(p.commits / maxCommits) * 100}%` }}
                  />
                </span>
                <span className="contrib-count">{p.commits.toLocaleString()}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

// ---- Hotspots tab -------------------------------------------------------

/** Hotspots tab: a table of high-churn files with churn percentile, commit counts, owner, and bus factor. */
function HotspotsTab({ repoId }: { repoId: string }) {
  const hotspots = useAsync(() => fetchHotspots(repoId), [repoId]);
  return (
    <section className="panel">
      <AsyncView state={hotspots}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted small">No hotspots — needs git history with churn.</p>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>File</th>
                  <th className="num">Churn pct</th>
                  <th className="num">90d commits</th>
                  <th className="num">Total</th>
                  <th>Owner</th>
                  <th className="num">Bus factor</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((h, i) => (
                  <tr key={i}>
                    <td className="mono path" title={h.path}>
                      {h.path}
                    </td>
                    <td className="num">{(h.churnPercentile * 100).toFixed(0)}%</td>
                    <td className="num">{h.commitCount90d}</td>
                    <td className="num">{h.commitCountTotal}</td>
                    <td>{h.primaryOwner ?? "—"}</td>
                    <td className="num">{h.busFactor}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        }
      </AsyncView>
    </section>
  );
}

// ---- Modules tab --------------------------------------------------------

/** Modules tab: a table of indexed modules with file/symbol/dependency counts and docs coverage. */
function ModulesTab({ repoId }: { repoId: string }) {
  const modules = useAsync(() => fetchModules(repoId), [repoId]);
  return (
    <section className="panel">
      <AsyncView state={modules}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted small">No modules indexed.</p>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Module</th>
                  <th className="num">Files</th>
                  <th className="num">Symbols</th>
                  <th className="num">Deps</th>
                  <th className="num">Docs coverage</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((m) => (
                  <tr key={m.name}>
                    <td className="mono path" title={m.name}>
                      {m.name}
                    </td>
                    <td className="num">{m.files}</td>
                    <td className="num">{m.symbols}</td>
                    <td className="num">{m.deps}</td>
                    <td className="num">{(m.docsCoverage * 100).toFixed(0)}%</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        }
      </AsyncView>
    </section>
  );
}

// ---- Dead Code tab ------------------------------------------------------

/** Dead Code tab: a table of unreachable files/symbols with confidence and a safe-to-delete flag. */
function DeadCodeTab({ repoId }: { repoId: string }) {
  const deadcode = useAsync(() => fetchDeadCode(repoId), [repoId]);
  return (
    <section className="panel">
      <AsyncView state={deadcode}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted small">No dead code detected.</p>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Kind</th>
                  <th>File</th>
                  <th>Symbol</th>
                  <th className="num">Confidence</th>
                  <th>Safe?</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((d, i) => (
                  <tr key={i}>
                    <td className="mono">{d.kind}</td>
                    <td className="mono path" title={d.filePath}>
                      {d.filePath}
                    </td>
                    <td className="mono">
                      {d.symbolName ?? "—"}
                      {d.symbolKind ? ` (${d.symbolKind})` : ""}
                    </td>
                    <td className="num">{(d.confidence * 100).toFixed(0)}%</td>
                    <td>
                      {d.safeToDelete ? (
                        <span className="add">safe</span>
                      ) : (
                        <span className="muted">review</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        }
      </AsyncView>
    </section>
  );
}

// ---- Impact tab: module coupling heatmap --------------------------------

/** Impact tab: a module-coupling heatmap used as a blast-radius proxy for changes. */
function ImpactTab({ repoId }: { repoId: string }) {
  const matrix = useAsync(() => fetchDependencyMatrix(repoId), [repoId]);
  return (
    <section className="panel">
      <header className="panel-head">
        <h3>Module coupling (blast radius proxy)</h3>
      </header>
      <AsyncView state={matrix}>
        {(m) =>
          m.modules.length === 0 ? (
            <p className="muted small">No module dependency data.</p>
          ) : (
            <Heatmap modules={m.modules} cells={m.cells} />
          )
        }
      </AsyncView>
    </section>
  );
}

// ---- Security tab -------------------------------------------------------

/** Maps a finding severity to a CSS color variable: bad for critical/high, warn for medium, muted otherwise. */
function severityColor(sev: string): string {
  const s = sev.toLowerCase();
  if (s === "critical" || s === "high") return "var(--bad)";
  if (s === "medium") return "var(--warn)";
  return "var(--muted)";
}

/** Security tab: a table of security findings with severity badge, kind, file, line, and snippet. */
function SecurityTab({ repoId }: { repoId: string }) {
  const security = useAsync(() => fetchSecurity(repoId), [repoId]);
  return (
    <section className="panel">
      <AsyncView state={security}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted small">No security findings.</p>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Severity</th>
                  <th>Kind</th>
                  <th>File</th>
                  <th className="num">Line</th>
                  <th>Snippet</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((f, i) => (
                  <tr key={i}>
                    <td>
                      <span
                        className="sev-badge"
                        style={{ background: severityColor(f.severity) }}
                      >
                        {f.severity}
                      </span>
                    </td>
                    <td className="mono">{f.kind}</td>
                    <td className="mono path" title={f.filePath}>
                      {f.filePath}
                    </td>
                    <td className="num">{f.line || "—"}</td>
                    <td className="mono reason" title={f.snippet}>
                      {f.snippet}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        }
      </AsyncView>
    </section>
  );
}

/** A single labeled stat card showing a localized numeric value, a sub-caption, and an icon. */
function StatCard({
  label,
  value,
  sub,
  icon,
}: {
  label: string;
  value: number;
  sub: string;
  icon: string;
}) {
  return (
    <div className="stat-card">
      <div className="stat-card-top">
        <span className="stat-card-label">{label}</span>
        <span className="stat-card-icon">
          <Icon name={icon} size={18} />
        </span>
      </div>
      <span className="stat-card-value">{value.toLocaleString()}</span>
      <span className="stat-card-sub muted">{sub}</span>
    </div>
  );
}
