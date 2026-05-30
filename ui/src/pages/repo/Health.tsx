import { useParams } from "react-router-dom";
import { fetchHealthFindings, fetchHealthHistory, fetchHealthSummary } from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";
import { MetricLabel, MetricTip } from "../../MetricTip.tsx";
import { TrendChart } from "../../charts.tsx";

export function RepoHealth() {
  const { repoId = "" } = useParams();
  const summary = useAsync(() => fetchHealthSummary(repoId), [repoId]);
  const findings = useAsync(() => fetchHealthFindings(repoId), [repoId]);
  const history = useAsync(() => fetchHealthHistory(repoId), [repoId]);

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
