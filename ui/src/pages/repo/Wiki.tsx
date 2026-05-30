import { useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { fetchPage, fetchPages, type WikiPage } from "../../api.ts";
import { AsyncView, useAsync } from "../../useAsync.tsx";
import { Icon } from "../../Icon.tsx";

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

// Filter pills for the STATUS (freshness) row. "" == All.
const FRESHNESS_FILTERS = ["", "fresh", "stale", "outdated"];

// Human label per page kind, used by the TYPE filter pills.
const KIND_LABELS: Record<string, string> = {
  architecture: "Overview",
  directory_overview: "Module",
  file_overview: "File",
  symbol_detail: "Symbol",
};

function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind.replace(/_/g, " ");
}

// Order the TYPE pills follow Overview -> Module -> File -> Symbol -> rest.
const KIND_ORDER = ["architecture", "directory_overview", "file_overview", "symbol_detail"];

function orderedKinds(pages: WikiPage[]): string[] {
  const present = new Set(pages.map((p) => p.pageType));
  const out: string[] = [];
  for (const k of KIND_ORDER) if (present.has(k)) out.push(k);
  for (const k of Array.from(present).sort()) if (!out.includes(k)) out.push(k);
  return out;
}

// The "By domain" tree groups page kinds into three domains. Each domain owns
// one or more kinds; a section renders only when it has matching pages.
type Domain = {
  key: string;
  label: string;
  glyph: string;
  kinds: string[];
  defaultOpen: boolean;
};

const DOMAINS: Domain[] = [
  { key: "tour", label: "Guided Tour", glyph: "explore", kinds: ["architecture"], defaultOpen: true },
  { key: "modules", label: "Modules", glyph: "widgets", kinds: ["directory_overview"], defaultOpen: false },
  {
    key: "reference",
    label: "Reference",
    glyph: "description",
    kinds: ["file_overview", "symbol_detail"],
    defaultOpen: false,
  },
];

type ViewMode = "folder" | "domain";

// A file entry inside the Reference domain, with its nested symbol pages.
type RefFile = { path: string; filePage?: WikiPage; symbols: WikiPage[] };

function WikiBrowser({ repoId, pages }: { repoId: string; pages: WikiPage[] }) {
  const [filter, setFilter] = useState("");
  const [freshness, setFreshness] = useState<string>("");
  const [kindFilter, setKindFilter] = useState<string>("");
  const [view, setView] = useState<ViewMode>("domain");
  const [showFilters, setShowFilters] = useState(true);
  // Default selection: the architecture page if present, else the first page.
  const [selectedId, setSelectedId] = useState<string>(
    () => (pages.find((p) => p.pageType === "architecture") ?? pages[0])?.id ?? "",
  );
  // Domain section collapse state, seeded from each domain's default.
  const [openDomains, setOpenDomains] = useState<Set<string>>(
    () => new Set(DOMAINS.filter((d) => d.defaultOpen).map((d) => d.key)),
  );
  const toggleDomain = (key: string) =>
    setOpenDomains((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  const q = filter.toLowerCase();
  const matches = (p: WikiPage) =>
    (!freshness || p.freshness === freshness) &&
    (!kindFilter || p.pageType === kindFilter) &&
    (!q ||
      p.targetPath.toLowerCase().includes(q) ||
      p.title.toLowerCase().includes(q));

  // Count summary reflects the FULL page set (not the filtered subset).
  const summary = useMemo(() => {
    let fresh = 0;
    let needAttention = 0;
    for (const p of pages) {
      if (p.freshness === "fresh") fresh++;
      else if (p.freshness === "stale" || p.freshness === "outdated") needAttention++;
    }
    return { total: pages.length, fresh, needAttention };
  }, [pages]);

  // TYPE pills: one per kind present in the data.
  const kinds = useMemo(() => orderedKinds(pages), [pages]);

  // ---- By domain data ----------------------------------------------------
  const visible = useMemo(
    () => pages.filter(matches),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [pages, filter, freshness, kindFilter],
  );

  const byKind = useMemo(() => {
    const m = new Map<string, WikiPage[]>();
    for (const p of visible) {
      const list = m.get(p.pageType);
      if (list) list.push(p);
      else m.set(p.pageType, [p]);
    }
    return m;
  }, [visible]);

  // Reference files: file_overview pages with symbol_detail pages nested under
  // their owning file (symbol targetPath split on "::"). Files with only
  // symbols still appear as a container row.
  const refFiles = useMemo(() => {
    const map = new Map<string, RefFile>();
    const get = (path: string) => {
      let f = map.get(path);
      if (!f) {
        f = { path, symbols: [] };
        map.set(path, f);
      }
      return f;
    };
    for (const p of byKind.get("file_overview") ?? []) get(p.targetPath).filePage = p;
    for (const p of byKind.get("symbol_detail") ?? []) {
      const [filePath] = p.targetPath.split("::");
      get(filePath).symbols.push(p);
    }
    const files = Array.from(map.values()).sort((a, b) => a.path.localeCompare(b.path));
    for (const f of files) f.symbols.sort((a, b) => a.targetPath.localeCompare(b.targetPath));
    return files;
  }, [byKind]);

  // ---- By folder data ----------------------------------------------------
  const tree = useMemo(() => buildTree(visible), [visible]);

  const selected = useMemo(
    () => pages.find((p) => p.id === selectedId),
    [pages, selectedId],
  );

  // Which domains have content under the current filters.
  const domainHasContent = (d: Domain): boolean => {
    if (d.key === "reference") return refFiles.length > 0;
    return d.kinds.some((k) => (byKind.get(k)?.length ?? 0) > 0);
  };
  const activeDomains = DOMAINS.filter(domainHasContent);

  const hasResults =
    view === "domain" ? activeDomains.length > 0 : tree.children.size > 0;

  const pageRow = (p: WikiPage, indent: number) => (
    <button
      key={p.id}
      className={"wiki-page-item" + (p.id === selectedId ? " wiki-page-item-on" : "")}
      onClick={() => setSelectedId(p.id)}
      title={p.targetPath}
      style={{ paddingLeft: 8 + indent }}
    >
      <span className="wiki-page-lines">
        <span className="wiki-page-title">{p.targetPath}</span>
        {p.title && <span className="wiki-page-sub">{p.title}</span>}
      </span>
      <span className={`fresh-dot fresh-${p.freshness}`} title={p.freshness} />
    </button>
  );

  return (
    <div className="wiki">
      <div className="wiki-list">
        <div className="wiki-types">
          <button
            className={"chip-btn" + (view === "domain" ? " chip-btn-on" : "")}
            onClick={() => setView("domain")}
          >
            <span className="wiki-glyph">
              <Icon name="account_tree" size={16} />
            </span>{" "}
            By domain
          </button>
          <button
            className={"chip-btn" + (view === "folder" ? " chip-btn-on" : "")}
            onClick={() => setView("folder")}
          >
            <span className="wiki-glyph">
              <Icon name="folder" size={16} />
            </span>{" "}
            By folder
          </button>
        </div>

        <div className="wiki-searchbar">
          <input
            className="wiki-search"
            placeholder="Search docs…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          <button
            className={"wiki-funnel" + (showFilters ? " wiki-funnel-on" : "")}
            onClick={() => setShowFilters((v) => !v)}
            title={showFilters ? "Hide filters" : "Show filters"}
            aria-label="Toggle filters"
          >
            <Icon name="filter_list" size={18} />
          </button>
        </div>

        {showFilters && (
          <div className="wiki-filters">
            <div className="wiki-filter-group">
              <div className="wiki-filter-label">Type</div>
              <div className="wiki-types">
                <button
                  className={"chip-btn" + (kindFilter === "" ? " chip-btn-on" : "")}
                  onClick={() => setKindFilter("")}
                >
                  All
                </button>
                {kinds.map((k) => (
                  <button
                    key={k}
                    className={"chip-btn" + (kindFilter === k ? " chip-btn-on" : "")}
                    onClick={() => setKindFilter(k)}
                  >
                    {kindLabel(k)}
                  </button>
                ))}
              </div>
            </div>
            <div className="wiki-filter-group">
              <div className="wiki-filter-label">Status</div>
              <div className="wiki-types">
                {FRESHNESS_FILTERS.map((f) => (
                  <button
                    key={f || "all"}
                    className={"chip-btn" + (freshness === f ? " chip-btn-on" : "")}
                    onClick={() => setFreshness(f)}
                  >
                    {f ? f.charAt(0).toUpperCase() + f.slice(1) : "All"}
                  </button>
                ))}
              </div>
            </div>
          </div>
        )}

        <div className="muted small wiki-summary">
          {summary.total} pages · {summary.fresh} fresh · {summary.needAttention} need attention
        </div>

        <div className="wiki-pages">
          {view === "domain" ? (
            activeDomains.map((d, i) => {
              const open = openDomains.has(d.key);
              return (
                <div key={d.key} className="wiki-section">
                  <button
                    className="wiki-domain-head"
                    onClick={() => toggleDomain(d.key)}
                    title={open ? "Collapse" : "Expand"}
                  >
                    <span className="wiki-domain-chevron">
                      <Icon name={open ? "expand_more" : "chevron_right"} size={18} />
                    </span>
                    <span className="wiki-domain-num">{i + 1}</span>
                    <span className="wiki-domain-glyph">
                      <Icon name={d.glyph} size={18} />
                    </span>
                    <span className="wiki-domain-label">{d.label}</span>
                  </button>
                  {open && (
                    <>
                      {d.key === "reference"
                        ? refFiles.map((f) => (
                            <div key={f.path} className="wiki-ref-file">
                              {f.filePage
                                ? pageRow(f.filePage, 12)
                                : (
                                  <div
                                    className="wiki-page-item wiki-ref-stub"
                                    style={{ paddingLeft: 20 }}
                                    title={f.path}
                                  >
                                    <span className="wiki-page-lines">
                                      <span className="wiki-page-title">{f.path}</span>
                                    </span>
                                  </div>
                                )}
                              {f.symbols.map((s) => pageRow(s, 24))}
                            </div>
                          ))
                        : d.kinds.flatMap((k) =>
                            (byKind.get(k) ?? [])
                              .slice()
                              .sort((a, b) => a.targetPath.localeCompare(b.targetPath))
                              .map((p) => pageRow(p, 12)),
                          )}
                    </>
                  )}
                </div>
              );
            })
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
              <Icon name={open ? "expand_more" : "chevron_right"} size={16} />
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
            <Icon name="description" size={16} />
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
                  <Icon name="data_object" size={16} />
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
          <Icon name={open ? "expand_more" : "chevron_right"} size={16} />
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
          <Icon name={open ? "folder_open" : "folder"} size={16} />
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
