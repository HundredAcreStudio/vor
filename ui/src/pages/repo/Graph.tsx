import { useParams, useSearchParams } from "react-router-dom";
import { fetchDependencyMatrix, fetchGraphEdges, fetchGraphNodes } from "../../api.ts";
import { ModuleGraph } from "../../ModuleGraph.tsx";
import { AsyncView, useAsync } from "../../useAsync.tsx";

export function Graph() {
  const { repoId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const focus = params.get("focus") ?? undefined;
  const matrix = useAsync(() => fetchDependencyMatrix(repoId), [repoId]);
  const files = useAsync(() => fetchGraphNodes(repoId, "file", 50), [repoId]);
  const edges = useAsync(() => fetchGraphEdges(repoId, "imports", 100), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>Graph</h1>
      </header>

      <p className="muted small">
        Module dependency graph — node size reflects connectivity; click a module to focus its
        imports.{focus ? ` Focused on ${focus}.` : ""}
      </p>
      <AsyncView state={matrix}>
        {(m) =>
          m.modules.length === 0 ? (
            <p className="muted">No import edges to graph.</p>
          ) : (
            <div className="module-graph-wrap">
              <ModuleGraph
                modules={m.modules}
                cells={m.cells}
                focus={focus}
                onSelect={(mod) => setParams(focus === mod ? {} : { focus: mod })}
              />
            </div>
          )
        }
      </AsyncView>

      <h2 className="section-title">Most central files</h2>
      <AsyncView state={files}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted">No graph nodes.</p>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>File</th>
                  <th>Language</th>
                  <th className="num">Symbols</th>
                  <th className="num">PageRank</th>
                  <th className="num">Betweenness</th>
                  <th className="num">Community</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((n) => (
                  <tr key={n.nodeId}>
                    <td className="mono path" title={n.filePath || n.nodeId}>
                      {n.filePath || n.nodeId}
                    </td>
                    <td>{n.language || "—"}</td>
                    <td className="num">{n.symbolCount ?? 0}</td>
                    <td className="num">{(n.pagerank ?? 0).toFixed(4)}</td>
                    <td className="num">{(n.betweenness ?? 0).toFixed(4)}</td>
                    <td className="num">{n.communityId ?? "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        }
      </AsyncView>

      <h2 className="section-title">Import edges</h2>
      <AsyncView state={edges}>
        {(rows) =>
          rows.length === 0 ? (
            <p className="muted">No import edges.</p>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>From</th>
                  <th>To</th>
                  <th>Imported</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((e, i) => (
                  <tr key={i}>
                    <td className="mono path" title={e.source}>
                      {e.source}
                    </td>
                    <td className="mono path" title={e.target}>
                      {e.target}
                    </td>
                    <td className="mono muted">{(e.importedNames ?? []).join(", ") || "—"}</td>
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
