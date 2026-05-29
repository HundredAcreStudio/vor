import { useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { fetchPage, fetchPages, type WikiPage } from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";

export function Wiki() {
  const { repoId = "" } = useParams();
  const pages = useAsync(() => fetchPages(repoId), [repoId]);

  return (
    <>
      <header className="page-header">
        <h1>Wiki</h1>
      </header>
      <AsyncView state={pages}>
        {(all) =>
          all.length === 0 ? (
            <p className="muted">No wiki pages generated. Run doc generation to populate this.</p>
          ) : (
            <WikiBrowser repoId={repoId} pages={all} />
          )
        }
      </AsyncView>
    </>
  );
}

function WikiBrowser({ repoId, pages }: { repoId: string; pages: WikiPage[] }) {
  const [filter, setFilter] = useState("");
  const [type, setType] = useState<string>("");
  const [selected, setSelected] = useState<string>(pages[0]?.targetPath ?? "");

  const types = useMemo(
    () => Array.from(new Set(pages.map((p) => p.pageType))).sort(),
    [pages],
  );

  const visible = useMemo(() => {
    const q = filter.toLowerCase();
    return pages.filter(
      (p) =>
        (!type || p.pageType === type) &&
        (!q || p.targetPath.toLowerCase().includes(q) || p.title.toLowerCase().includes(q)),
    );
  }, [pages, filter, type]);

  return (
    <div className="wiki">
      <div className="wiki-list">
        <input
          className="wiki-search"
          placeholder="Search docs…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <div className="wiki-types">
          <button className={"chip-btn" + (type === "" ? " chip-btn-on" : "")} onClick={() => setType("")}>
            All
          </button>
          {types.map((t) => (
            <button
              key={t}
              className={"chip-btn" + (type === t ? " chip-btn-on" : "")}
              onClick={() => setType(t)}
            >
              {t.replace(/_/g, " ")}
            </button>
          ))}
        </div>
        <div className="wiki-pages">
          {visible.map((p) => (
            <button
              key={p.id}
              className={"wiki-page-item" + (p.targetPath === selected ? " wiki-page-item-on" : "")}
              onClick={() => setSelected(p.targetPath)}
              title={p.targetPath}
            >
              <span className="wiki-page-title">{p.title || p.targetPath}</span>
              <span className={`fresh-dot fresh-${p.freshness}`} title={p.freshness} />
            </button>
          ))}
          {visible.length === 0 && <p className="muted small">No matches.</p>}
        </div>
      </div>
      <div className="wiki-doc">
        {selected ? <WikiDoc repoId={repoId} path={selected} /> : <p className="muted">Select a page.</p>}
      </div>
    </div>
  );
}

function WikiDoc({ repoId, path }: { repoId: string; path: string }) {
  const doc = useAsync(() => fetchPage(repoId, path), [repoId, path]);
  return (
    <AsyncView state={doc}>
      {(d) => (
        <>
          <div className="wiki-doc-head">
            <span className={`badge fresh-${d.freshness}`}>{d.freshness}</span>
            {d.modelName && <span className="badge">{d.modelName}</span>}
          </div>
          <article className="markdown">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{d.content}</ReactMarkdown>
          </article>
        </>
      )}
    </AsyncView>
  );
}
