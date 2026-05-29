import { useParams } from "react-router-dom";
import { fetchDeadCode } from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";

export function RepoDeadCode() {
  const { repoId = "" } = useParams();
  const deadcode = useAsync(() => fetchDeadCode(repoId), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>Dead code</h1>
      </header>
      <AsyncView state={deadcode}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted">No dead code detected.</p>
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
    </>
  );
}
