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

// Ordered, labeled sections keyed by pageType. Anything not listed here gets a
// section derived from its kind (underscores -> spaces). Overview is pinned
// first so the architecture page anchors the tree.
const SECTION_ORDER: { kind: string; label: string }[] = [
  { kind: "architecture", label: "Overview" },
  { kind: "directory_overview", label: "Modules" },
  { kind: "file_overview", label: "Files" },
  { kind: "symbol_detail", label: "Symbols" },
];

type Section = { kind: string; label: string; pages: WikiPage[] };

function groupSections(pages: WikiPage[]): Section[] {
  const byKind = new Map<string, WikiPage[]>();
  for (const p of pages) {
    const list = byKind.get(p.pageType);
    if (list) list.push(p);
    else byKind.set(p.pageType, [p]);
  }

  const sections: Section[] = [];
  const seen = new Set<string>();
  for (const { kind, label } of SECTION_ORDER) {
    const ps = byKind.get(kind);
    if (ps && ps.length) {
      sections.push({ kind, label, pages: ps });
      seen.add(kind);
    }
  }
  // Unknown kinds, in stable alphabetical order.
  for (const kind of Array.from(byKind.keys()).sort()) {
    if (seen.has(kind)) continue;
    const ps = byKind.get(kind)!;
    sections.push({ kind, label: kind.replace(/_/g, " "), pages: ps });
  }

  // Sort within each section by targetPath ascending.
  for (const s of sections) {
    s.pages = [...s.pages].sort((a, b) => a.targetPath.localeCompare(b.targetPath));
  }
  return sections;
}

const FRESHNESS_FILTERS = ["", "fresh", "stale", "outdated"];

function WikiBrowser({ repoId, pages }: { repoId: string; pages: WikiPage[] }) {
  const [filter, setFilter] = useState("");
  const [freshness, setFreshness] = useState<string>("");
  // Default selection: the architecture page if present, else the first page.
  const [selectedId, setSelectedId] = useState<string>(
    () => (pages.find((p) => p.pageType === "architecture") ?? pages[0])?.id ?? "",
  );

  const sections = useMemo(() => {
    const q = filter.toLowerCase();
    return groupSections(pages)
      .map((s) => ({
        ...s,
        pages: s.pages.filter(
          (p) =>
            (!freshness || p.freshness === freshness) &&
            (!q ||
              p.targetPath.toLowerCase().includes(q) ||
              p.title.toLowerCase().includes(q)),
        ),
      }))
      .filter((s) => s.pages.length > 0);
  }, [pages, filter, freshness]);

  const selected = useMemo(
    () => pages.find((p) => p.id === selectedId),
    [pages, selectedId],
  );

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
          {FRESHNESS_FILTERS.map((f) => (
            <button
              key={f || "all"}
              className={"chip-btn" + (freshness === f ? " chip-btn-on" : "")}
              onClick={() => setFreshness(f)}
            >
              {f || "All"}
            </button>
          ))}
        </div>
        <div className="wiki-pages">
          {sections.map((s) => (
            <div key={s.kind} className="wiki-section">
              <div className="wiki-section-head">
                {s.label}
                <span className="wiki-section-count">{s.pages.length}</span>
              </div>
              {s.pages.map((p) => {
                const isOverview = p.pageType === "architecture";
                return (
                  <button
                    key={p.id}
                    className={
                      "wiki-page-item" + (p.id === selectedId ? " wiki-page-item-on" : "")
                    }
                    onClick={() => setSelectedId(p.id)}
                    title={p.targetPath}
                  >
                    <span className="wiki-page-lines">
                      <span className="wiki-page-title">
                        {isOverview ? p.title || p.targetPath : p.targetPath}
                      </span>
                      {!isOverview && p.title && (
                        <span className="wiki-page-sub">{p.title}</span>
                      )}
                    </span>
                    <span className={`fresh-dot fresh-${p.freshness}`} title={p.freshness} />
                  </button>
                );
              })}
            </div>
          ))}
          {sections.length === 0 && <p className="muted small">No matches.</p>}
        </div>
      </div>
      <div className="wiki-doc">
        {selected ? (
          <WikiDoc
            repoId={repoId}
            targetPath={selected.targetPath}
            pageType={selected.pageType}
          />
        ) : (
          <p className="muted">Select a page.</p>
        )}
      </div>
    </div>
  );
}

function WikiDoc({
  repoId,
  targetPath,
  pageType,
}: {
  repoId: string;
  targetPath: string;
  pageType: string;
}) {
  const doc = useAsync(
    () => fetchPage(repoId, targetPath, pageType),
    [repoId, targetPath, pageType],
  );
  return (
    <AsyncView state={doc}>
      {(d) => (
        <>
          {d.pageType !== "architecture" && (
            <div className="wiki-doc-path">{d.targetPath}</div>
          )}
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
