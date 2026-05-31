import { useParams } from "react-router-dom";
import { fetchGraphNodes } from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";

/** Renders the repo Symbols page: the most-central indexed symbols (graph nodes ranked by PageRank). */
export function Symbols() {
  const { repoId = "" } = useParams();
  // No dedicated symbol-list endpoint; graph_nodes filtered to symbols and
  // ranked by PageRank gives the most-central symbols first.
  const symbols = useAsync(() => fetchGraphNodes(repoId, "symbol", 200), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>Symbols</h1>
      </header>
      <AsyncView state={symbols}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted">No symbols indexed.</p>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Kind</th>
                  <th>Visibility</th>
                  <th>File</th>
                  <th className="num">PageRank</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((n) => (
                  <tr key={n.nodeId}>
                    <td className="mono">{n.name || n.nodeId}</td>
                    <td>{n.kind || "—"}</td>
                    <td className="muted">{n.visibility || "—"}</td>
                    <td className="mono path" title={n.filePath}>
                      {n.filePath}
                      {n.startLine ? `:${n.startLine}` : ""}
                    </td>
                    <td className="num">{(n.pagerank ?? 0).toFixed(4)}</td>
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
