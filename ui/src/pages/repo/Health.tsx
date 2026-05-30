import { useParams } from "react-router-dom";
import { fetchHealthFindings, fetchHealthSummary } from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";
import { MetricLabel, MetricTip } from "../../MetricTip.tsx";

export function RepoHealth() {
  const { repoId = "" } = useParams();
  const summary = useAsync(() => fetchHealthSummary(repoId), [repoId]);
  const findings = useAsync(() => fetchHealthFindings(repoId), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>Health</h1>
      </header>

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
