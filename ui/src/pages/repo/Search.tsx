import { useState } from "react";
import { useParams } from "react-router-dom";
import { searchSymbols, type SearchHit } from "../../api.ts";

export function Search() {
  const { repoId = "" } = useParams();
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<SearchHit[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function run(e: React.FormEvent) {
    e.preventDefault();
    const term = q.trim();
    if (!term) return;
    setBusy(true);
    setError(null);
    searchSymbols(repoId, term)
      .then((m) => setHits(m))
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false));
  }

  return (
    <>
      <header className="page-header">
        <h1>Search</h1>
      </header>
      <form className="search-bar" onSubmit={run}>
        <input
          autoFocus
          className="wiki-search"
          placeholder="Search symbols, files, qualified names…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <button className="btn" type="submit" disabled={busy}>
          {busy ? "Searching…" : "Search"}
        </button>
      </form>

      {error && <p className="error">Failed: {error}</p>}
      {hits !== null && !error && (
        <p className="muted small">
          {hits.length} result{hits.length === 1 ? "" : "s"}
        </p>
      )}
      {hits && hits.length > 0 && (
        <table className="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Kind</th>
              <th>File</th>
              <th className="num">PageRank</th>
            </tr>
          </thead>
          <tbody>
            {hits.map((h) => (
              <tr key={h.nodeId}>
                <td className="mono">{h.name || h.nodeId}</td>
                <td>{h.kind || h.nodeType}</td>
                <td className="mono path" title={h.filePath}>
                  {h.filePath}
                  {h.startLine ? `:${h.startLine}` : ""}
                </td>
                <td className="num">{h.pagerank.toFixed(4)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
