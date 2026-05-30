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

type ViewMode = "folder" | "type";

function WikiBrowser({ repoId, pages }: { repoId: string; pages: WikiPage[] }) {
  const [filter, setFilter] = useState("");
  const [freshness, setFreshness] = useState<string>("");
  const [view, setView] = useState<ViewMode>("folder");
  // Default selection: the architecture page if present, else the first page.
  const [selectedId, setSelectedId] = useState<string>(
    () => (pages.find((p) => p.pageType === "architecture") ?? pages[0])?.id ?? "",
  );

  // The architecture / overview page is pinned at the very top in both views.
  const overview = useMemo(
    () => pages.find((p) => p.pageType === "architecture"),
    [pages],
  );
  // Everything else feeds the type sections / folder tree.
  const rest = useMemo(
    () => pages.filter((p) => p.pageType !== "architecture"),
    [pages],
  );

  const q = filter.toLowerCase();
  const matches = (p: WikiPage) =>
    (!freshness || p.freshness === freshness) &&
    (!q ||
      p.targetPath.toLowerCase().includes(q) ||
      p.title.toLowerCase().includes(q));

  const overviewVisible = overview ? matches(overview) : false;

  const sections = useMemo(() => {
    return groupSections(rest)
      .map((s) => ({ ...s, pages: s.pages.filter(matches) }))
      .filter((s) => s.pages.length > 0);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rest, filter, freshness]);

  const tree = useMemo(() => buildTree(rest.filter(matches)), [
    // eslint-disable-next-line react-hooks/exhaustive-deps
    rest,
    filter,
    freshness,
  ]);

  const selected = useMemo(
    () => pages.find((p) => p.id === selectedId),
    [pages, selectedId],
  );

  const hasResults =
    overviewVisible || (view === "type" ? sections.length > 0 : tree.children.size > 0);

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
          <button
            className={"chip-btn" + (view === "folder" ? " chip-btn-on" : "")}
            onClick={() => setView("folder")}
          >
            By folder
          </button>
          <button
            className={"chip-btn" + (view === "type" ? " chip-btn-on" : "")}
            onClick={() => setView("type")}
          >
            By type
          </button>
        </div>
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
          {overview && overviewVisible && (
            <button
              key={overview.id}
              className={
                "wiki-page-item wiki-overview-item" +
                (overview.id === selectedId ? " wiki-page-item-on" : "")
              }
              onClick={() => setSelectedId(overview.id)}
              title={overview.title || overview.targetPath}
            >
              <span className="wiki-page-lines">
                <span className="wiki-page-title">{overview.title || "Overview"}</span>
              </span>
              <span
                className={`fresh-dot fresh-${overview.freshness}`}
                title={overview.freshness}
              />
            </button>
          )}

          {view === "type" ? (
            sections.map((s) => (
              <div key={s.kind} className="wiki-section">
                <div className="wiki-section-head">
                  {s.label}
                  <span className="wiki-section-count">{s.pages.length}</span>
                </div>
                {s.pages.map((p) => (
                  <button
                    key={p.id}
                    className={
                      "wiki-page-item" + (p.id === selectedId ? " wiki-page-item-on" : "")
                    }
                    onClick={() => setSelectedId(p.id)}
                    title={p.targetPath}
                  >
                    <span className="wiki-page-lines">
                      <span className="wiki-page-title">{p.targetPath}</span>
                      {p.title && <span className="wiki-page-sub">{p.title}</span>}
                    </span>
                    <span className={`fresh-dot fresh-${p.freshness}`} title={p.freshness} />
                  </button>
                ))}
              </div>
            ))
          ) : (
            <FolderTree
              root={tree}
              selectedId={selectedId}
              onSelect={setSelectedId}
              searching={q.length > 0}
            />
          )}

          {!hasResults && <p className="muted small">No matches.</p>}
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

// ---- folder tree ---------------------------------------------------------

type TreeNode = {
  // Full path used as a stable key + tooltip + expanded-state identity.
  path: string;
  // Last path segment, shown as the label.
  name: string;
  // A directory_overview page bound to this folder, if any (makes it selectable).
  dirPage?: WikiPage;
  // A file_overview page bound to this leaf, if any.
  filePage?: WikiPage;
  // symbol_detail pages owned by this file node.
  symbols: WikiPage[];
  children: Map<string, TreeNode>;
};

function newNode(path: string, name: string): TreeNode {
  return { path, name, symbols: [], children: new Map() };
}

// Build a nested folder tree from page targetPaths. file_overview pages become
// leaf nodes; directory_overview pages attach to their folder node; symbol_detail
// pages nest under their owning file node (split on "::"). File nodes are created
// as containers even when no file_overview page exists.
function buildTree(pages: WikiPage[]): TreeNode {
  const root = newNode("", "");

  // Walk segments, creating intermediate folder nodes; returns the final node.
  const descend = (segments: string[]): TreeNode => {
    let node = root;
    let acc = "";
    for (const seg of segments) {
      acc = acc ? `${acc}/${seg}` : seg;
      let child = node.children.get(seg);
      if (!child) {
        child = newNode(acc, seg);
        node.children.set(seg, child);
      }
      node = child;
    }
    return node;
  };

  for (const p of pages) {
    if (p.pageType === "directory_overview") {
      const node = descend(p.targetPath.split("/").filter(Boolean));
      node.dirPage = p;
    } else if (p.pageType === "file_overview") {
      const node = descend(p.targetPath.split("/").filter(Boolean));
      node.filePage = p;
    } else if (p.pageType === "symbol_detail") {
      const [filePath] = p.targetPath.split("::");
      const node = descend(filePath.split("/").filter(Boolean));
      node.symbols.push(p);
    } else {
      // Unknown kinds: treat targetPath as a path leaf so they remain reachable.
      const node = descend(p.targetPath.split("/").filter(Boolean));
      node.filePage = node.filePage ?? p;
    }
  }

  // Stable sort of symbols by name.
  const sortNode = (n: TreeNode) => {
    n.symbols.sort((a, b) => a.targetPath.localeCompare(b.targetPath));
    for (const c of n.children.values()) sortNode(c);
  };
  sortNode(root);
  return root;
}

// A node is a "file" (leaf) when it carries a file_overview page or symbols and
// has no child folders. Otherwise it's a folder container.
function isFileNode(n: TreeNode): boolean {
  return n.children.size === 0 && (!!n.filePage || n.symbols.length > 0);
}

// Sort children: folders first (alphabetical), then files (alphabetical).
function sortedChildren(n: TreeNode): TreeNode[] {
  return Array.from(n.children.values()).sort((a, b) => {
    const af = isFileNode(a) ? 1 : 0;
    const bf = isFileNode(b) ? 1 : 0;
    if (af !== bf) return af - bf;
    return a.name.localeCompare(b.name);
  });
}

function FolderTree({
  root,
  selectedId,
  onSelect,
  searching,
}: {
  root: TreeNode;
  selectedId: string;
  onSelect: (id: string) => void;
  searching: boolean;
}) {
  // Default expansion: top 1-2 levels expanded. While searching, every ancestor
  // of a match is part of the tree (we filtered first), so expand all.
  const [expanded, setExpanded] = useState<Set<string>>(() => {
    const init = new Set<string>();
    const seed = (n: TreeNode, depth: number) => {
      for (const c of n.children.values()) {
        if (!isFileNode(c) && depth < 2) init.add(c.path);
        seed(c, depth + 1);
      }
    };
    seed(root, 0);
    return init;
  });

  const toggle = (path: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  // When searching, the filtered tree is small; force everything visible.
  const isOpen = (path: string) => searching || expanded.has(path);

  return (
    <div className="wiki-tree">
      {sortedChildren(root).map((c) => (
        <TreeRow
          key={c.path}
          node={c}
          depth={0}
          selectedId={selectedId}
          onSelect={onSelect}
          isOpen={isOpen}
          toggle={toggle}
        />
      ))}
    </div>
  );
}

function TreeRow({
  node,
  depth,
  selectedId,
  onSelect,
  isOpen,
  toggle,
}: {
  node: TreeNode;
  depth: number;
  selectedId: string;
  onSelect: (id: string) => void;
  isOpen: (path: string) => boolean;
  toggle: (path: string) => void;
}) {
  const fileLeaf = isFileNode(node);

  // A file node is expandable if it owns symbols.
  if (fileLeaf) {
    const hasSymbols = node.symbols.length > 0;
    const open = isOpen(node.path);
    const page = node.filePage;
    return (
      <>
        <div className="wiki-tree-row" style={{ paddingLeft: depth * 12 }}>
          {hasSymbols ? (
            <button
              className="wiki-tree-caret"
              onClick={() => toggle(node.path)}
              title={open ? "Collapse" : "Expand"}
            >
              {open ? "▾" : "▸"}
            </button>
          ) : (
            <span className="wiki-tree-caret wiki-tree-caret-empty" />
          )}
          <button
            className={
              "wiki-page-item wiki-tree-item" +
              (page && page.id === selectedId ? " wiki-page-item-on" : "")
            }
            onClick={() => {
              if (page) onSelect(page.id);
              else if (hasSymbols) toggle(node.path);
            }}
            title={node.path}
            disabled={!page && !hasSymbols}
          >
            <span className="wiki-page-title">{node.name}</span>
            {page ? (
              <span className={`fresh-dot fresh-${page.freshness}`} title={page.freshness} />
            ) : null}
          </button>
        </div>
        {hasSymbols &&
          open &&
          node.symbols.map((s) => {
            const sym = s.targetPath.split("::")[1] ?? s.targetPath;
            return (
              <div
                key={s.id}
                className="wiki-tree-row"
                style={{ paddingLeft: (depth + 1) * 12 }}
              >
                <span className="wiki-tree-caret wiki-tree-caret-empty" />
                <button
                  className={
                    "wiki-page-item wiki-tree-item" +
                    (s.id === selectedId ? " wiki-page-item-on" : "")
                  }
                  onClick={() => onSelect(s.id)}
                  title={s.targetPath}
                >
                  <span className="wiki-page-title wiki-tree-symbol">{sym}</span>
                  <span className={`fresh-dot fresh-${s.freshness}`} title={s.freshness} />
                </button>
              </div>
            );
          })}
      </>
    );
  }

  // Folder node.
  const open = isOpen(node.path);
  const dir = node.dirPage;
  return (
    <>
      <div className="wiki-tree-row" style={{ paddingLeft: depth * 12 }}>
        <button
          className="wiki-tree-caret"
          onClick={() => toggle(node.path)}
          title={open ? "Collapse" : "Expand"}
        >
          {open ? "▾" : "▸"}
        </button>
        <button
          className={
            "wiki-page-item wiki-tree-item wiki-tree-folder" +
            (dir && dir.id === selectedId ? " wiki-page-item-on" : "")
          }
          onClick={() => {
            if (dir) onSelect(dir.id);
            else toggle(node.path);
          }}
          title={node.path}
        >
          <span className="wiki-page-title wiki-tree-folder-name">{node.name}</span>
          {dir ? (
            <span className={`fresh-dot fresh-${dir.freshness}`} title={dir.freshness} />
          ) : null}
        </button>
      </div>
      {open &&
        sortedChildren(node).map((c) => (
          <TreeRow
            key={c.path}
            node={c}
            depth={depth + 1}
            selectedId={selectedId}
            onSelect={onSelect}
            isOpen={isOpen}
            toggle={toggle}
          />
        ))}
    </>
  );
}

// ---- doc view ------------------------------------------------------------

function relTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const diff = Date.now() - t;
  const s = Math.round(diff / 1000);
  if (s < 60) return "just now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.round(h / 24);
  if (d < 30) return `${d}d ago`;
  const mo = Math.round(d / 30);
  if (mo < 12) return `${mo}mo ago`;
  return `${Math.round(mo / 12)}y ago`;
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
      {(d) => {
        const rel = relTime(d.updatedAt);
        const tokens =
          d.inputTokens != null || d.outputTokens != null
            ? `${d.inputTokens ?? 0} in · ${d.outputTokens ?? 0} out`
            : "";
        const metaParts = [`v${d.version}`, rel, tokens].filter(Boolean);
        return (
          <>
            {d.pageType !== "architecture" && (
              <div className="wiki-doc-path">{d.targetPath}</div>
            )}
            <div className="wiki-doc-head">
              <span className={`badge fresh-${d.freshness}`}>{d.freshness}</span>
              {d.modelName && <span className="badge">{d.modelName}</span>}
            </div>
            {metaParts.length > 0 && (
              <div className="muted small wiki-doc-meta">{metaParts.join(" · ")}</div>
            )}
            <article className="markdown">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{d.content}</ReactMarkdown>
            </article>
          </>
        );
      }}
    </AsyncView>
  );
}
