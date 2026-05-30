import { useParams } from "react-router-dom";
import {
  type FileDelta,
  type HealthDiff,
  fetchHealthDiff,
  fetchHealthFindings,
  fetchHealthHistory,
  fetchHealthSummary,
} from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";
import { Icon } from "../../Icon.tsx";
import { MetricLabel, MetricTip } from "../../MetricTip.tsx";
import { TrendChart } from "../../charts.tsx";

const DELTA_ROWS = 8;

function deltaColor(delta: number): string {
  if (delta <= -0.05) return "var(--bad)";
  if (delta >= 0.05) return "var(--good)";
  return "var(--muted)";
}

function fmtDelta(delta: number): string {
  const v = delta.toFixed(2).replace(/\.?0+$/, "");
  return delta > 0 ? `+${v}` : v;
}

function DeltaList({
  title,
  rows,
  color,
}: {
  title: string;
  rows: FileDelta[];
  color: string;
}) {
  return (
    <div className="diff-col">
      <h4 className="diff-col-title">
        {title} <span className="muted">({rows.length})</span>
      </h4>
      {rows.length === 0 ? (
        <p className="muted small">None.</p>
      ) : (
        <ul className="diff-rows">
          {rows.slice(0, DELTA_ROWS).map((r) => (
            <li key={r.path} className="diff-row">
              <span className="mono diff-path" title={r.path}>
                {r.path.split("/").pop() ?? r.path}
              </span>
              <span className="diff-nums mono" style={{ color }}>
                {r.before} → {r.after} ({fmtDelta(r.delta)})
              </span>
            </li>
          ))}
          {rows.length > DELTA_ROWS && (
            <li className="muted small">+{rows.length - DELTA_ROWS} more</li>
          )}
        </ul>
      )}
    </div>
  );
}

function DiffPanelBody({ d }: { d: HealthDiff }) {
  if (!d.hasBaseline) {
    return (
      <p className="muted small">
        No committed baseline yet — commit and re-index to compare.
      </p>
    );
  }
  const regressions = [...d.regressions].sort((a, b) => a.delta - b.delta);
  const improvements = [...d.improvements].sort((a, b) => b.delta - a.delta);
  const noChange = regressions.length === 0 && improvements.length === 0;
  const fileNotes: string[] = [];
  if (d.newFiles.length > 0)
    fileNotes.push(`${d.newFiles.length} new file${d.newFiles.length === 1 ? "" : "s"}`);
  if (d.removedFiles.length > 0)
    fileNotes.push(`${d.removedFiles.length} removed`);
  return (
    <>
      <div className="diff-summary">
        <Icon name={d.delta < 0 ? "trending_down" : "trending_up"} />
        <span className="mono">
          vs {d.baselineCommit.slice(0, 7)} ({d.baselineBranch}): {d.currentAvg.toFixed(1)}{" "}
          <b style={{ color: deltaColor(d.delta) }}>(Δ {fmtDelta(d.delta)})</b>
        </span>
      </div>
      {noChange ? (
        <p className="muted small">No health change since the last commit.</p>
      ) : (
        <div className="diff-cols">
          <DeltaList title="Regressed" rows={regressions} color="var(--bad)" />
          <DeltaList title="Improved" rows={improvements} color="var(--good)" />
        </div>
      )}
      {fileNotes.length > 0 && <p className="muted small">{fileNotes.join(", ")}</p>}
    </>
  );
}

export function RepoHealth() {
  const { repoId = "" } = useParams();
  const summary = useAsync(() => fetchHealthSummary(repoId), [repoId]);
  const findings = useAsync(() => fetchHealthFindings(repoId), [repoId]);
  const history = useAsync(() => fetchHealthHistory(repoId), [repoId]);
  const diff = useAsync(() => fetchHealthDiff(repoId), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>Health</h1>
      </header>

      <div className="panel trend-panel">
        <div className="panel-head">
          <h3>Health over time</h3>
        </div>
        <AsyncView state={history}>
          {(snaps) =>
            snaps.length === 0 ? (
              <p className="muted">
                No history yet — a snapshot is recorded per commit as the repo is re-indexed.
              </p>
            ) : (
              <>
                <TrendChart
                  series={[
                    {
                      label: "Average",
                      color: "var(--good)",
                      values: snaps.map((s) => s.average),
                    },
                    {
                      label: "Hotspot",
                      color: "var(--warn)",
                      values: snaps.map((s) => s.hotspot),
                    },
                  ]}
                  max={10}
                  xLabels={snaps.map((s) => s.commit.slice(0, 7))}
                />
                {snaps.length === 1 && (
                  <p className="muted">
                    Only one snapshot so far — more points appear as the repo is re-indexed.
                  </p>
                )}
                <p className="muted trend-foot mono">
                  latest {snaps[snaps.length - 1].commit.slice(0, 7)}
                </p>
              </>
            )
          }
        </AsyncView>
      </div>

      <div className="panel diff-panel">
        <div className="panel-head">
          <h3>Changes since last commit</h3>
        </div>
        <AsyncView state={diff}>{(d) => <DiffPanelBody d={d} />}</AsyncView>
      </div>

      <AsyncView state={summary}>
        {(s) => {
          const tier = s.averageScore >= 7.5 ? "good" : s.averageScore >= 5 ? "warn" : "bad";
          const biomarkers = Object.entries(s.findingsByBiomarker).sort((a, b) => b[1] - a[1]);
          return (
            <>
              <div className="kpi-row">
                <div className="kpi">
                  <span className={`kpi-value health-${tier}`}>{s.averageScore.toFixed(1)}</span>
                  <span className="kpi-label">avg score (1–10)</span>
                </div>
                <div className="kpi">
                  <span className="kpi-value">{s.findingCount.toLocaleString()}</span>
                  <span className="kpi-label">findings</span>
                </div>
              </div>
              {biomarkers.length > 0 && (
                <div className="chips">
                  {biomarkers.map(([kind, n]) => (
                    <MetricTip id={kind} key={kind}>
                      <span className="chip">
                        {kind.replace(/_/g, " ")}
                        <b>{n}</b>
                      </span>
                    </MetricTip>
                  ))}
                </div>
              )}
            </>
          );
        }}
      </AsyncView>

      <h2 className="section-title">Findings</h2>
      <AsyncView state={findings}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted">No health findings.</p>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Biomarker</th>
                  <th>File</th>
                  <th>Function</th>
                  <th className="num">Impact</th>
                  <th>Reason</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((f, i) => (
                  <tr key={i}>
                    <td>
                      <MetricLabel id={f.biomarkerType} />
                    </td>
                    <td className="mono path" title={f.filePath}>
                      {f.filePath}
                      {f.lineStart ? `:${f.lineStart}` : ""}
                    </td>
                    <td className="mono">{f.functionName ?? "—"}</td>
                    <td className="num">{f.healthImpact.toFixed(1)}</td>
                    <td className="reason">{f.reason}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        }
      </AsyncView>
    </>
  );
}
