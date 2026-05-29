import { useParams } from "react-router-dom";
import { fetchHotspots } from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";

export function RepoHotspots() {
  const { repoId = "" } = useParams();
  const hotspots = useAsync(() => fetchHotspots(repoId), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>Hotspots</h1>
      </header>
      <AsyncView state={hotspots}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted">No hotspots — needs git history with churn.</p>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>File</th>
                  <th className="num">90d commits</th>
                  <th className="num">Total</th>
                  <th>Owner</th>
                  <th className="num">Bus factor</th>
                  <th className="num">+/− (90d)</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((h, i) => (
                  <tr key={i}>
                    <td className="mono path" title={h.path}>
                      {h.path}
                    </td>
                    <td className="num">{h.commitCount90d}</td>
                    <td className="num">{h.commitCountTotal}</td>
                    <td>{h.primaryOwner ?? "—"}</td>
                    <td className="num">{h.busFactor}</td>
                    <td className="num">
                      <span className="add">+{h.linesAdded90d}</span>{" "}
                      <span className="del">−{h.linesDeleted90d}</span>
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
