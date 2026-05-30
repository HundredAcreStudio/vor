// Thin client over the vor daemon's REST API. In dev, Vite proxies /api to
// the daemon; in production the daemon serves this bundle, so relative URLs
// resolve to the same origin.

export type RepoSummary = {
  id: string;
  slug: string;
  name: string;
  localPath: string;
  headCommit: string;
  lastIndexed: string; // RFC3339
  fileCount: number;
  symbolCount: number;
  healthAvg: number;
  findingCount: number;
};

export type Overview = {
  repos: RepoSummary[];
};

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: { Accept: "application/json" } });
  if (!res.ok) {
    const detail = await res.text().catch(() => "");
    throw new Error(`${res.status} ${res.statusText}${detail ? `: ${detail}` : ""}`);
  }
  return (await res.json()) as T;
}

async function send<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const detail = await res.text().catch(() => "");
    throw new Error(`${res.status} ${res.statusText}${detail ? `: ${detail.trim()}` : ""}`);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

export function fetchOverview(): Promise<Overview> {
  return getJSON<Overview>("/api/overview");
}

// ---- biomarker catalog (global, static) --------------------------------

export type Biomarker = {
  id: string;
  name: string;
  scope: "function" | "class" | "file";
  summary: string;
  how: string;
  thresholds: string;
  severities: string[];
};

// Cached at the module level so the many MetricTip/MetricLabel instances that
// reference the catalog share a single in-flight/resolved request.
let _biomarkers: Promise<Biomarker[]> | null = null;

export function fetchBiomarkers(): Promise<Biomarker[]> {
  return (_biomarkers ??= getJSON<{ biomarkers: Biomarker[] }>("/api/biomarkers").then(
    (r) => r.biomarkers ?? [],
  ));
}

// ---- overview extras: attention digest + language mix ------------------

export type AttentionItem = {
  category: "knowledge_silo" | "ungoverned_hotspot" | "dead_code" | "needs_review";
  title: string;
  detail: string;
  link: string;
};

export function fetchAttention(id: string): Promise<AttentionItem[]> {
  return getJSON<{ items: AttentionItem[] }>(`/api/repos/${id}/attention`).then(
    (r) => r.items ?? [],
  );
}

export type LanguageSlice = { language: string; files: number };

export function fetchLanguages(id: string): Promise<{ languages: LanguageSlice[]; total: number }> {
  return getJSON<{ languages: LanguageSlice[]; total: number }>(`/api/repos/${id}/languages`);
}

export type Community = { communityId: number; label: string; size: number; top: string[] };

export function fetchCommunities(id: string): Promise<Community[]> {
  return getJSON<{ communities: Community[] }>(`/api/repos/${id}/communities`).then(
    (r) => r.communities ?? [],
  );
}

export type EntryPoint = { path: string; language?: string; pagerank: number };

export function fetchEntryPoints(id: string): Promise<EntryPoint[]> {
  return getJSON<{ entryPoints: EntryPoint[] }>(`/api/repos/${id}/entry-points`).then(
    (r) => r.entryPoints ?? [],
  );
}

export type BusFactor = {
  safe: number;
  warning: number;
  risk: number;
  total: number;
  riskFiles: { path: string; contributors: number }[];
};

export type GitInsights = {
  busFactor: BusFactor;
  churnBuckets: { label: string; count: number }[];
  contributors: { name: string; commits: number }[];
};

export function fetchGitInsights(id: string): Promise<GitInsights> {
  return getJSON<GitInsights>(`/api/repos/${id}/git-insights`);
}

export type RiskData = {
  counts: {
    hotspots: number;
    silos: number;
    deadCode: number;
    staleDecisions: number;
    securityHigh: number;
  };
  busFactor: BusFactor;
  churnBuckets: { label: string; count: number }[];
  topContributors: { name: string; commits: number }[];
};

export function fetchRisk(id: string): Promise<RiskData> {
  return getJSON<RiskData>(`/api/repos/${id}/risk`);
}

// ---- risk drill-downs: ownership treemap + contributor network ----------

export type OwnershipCell = {
  name: string;
  value: number; // size weight used for treemap area
  files: number;
  risk: number; // 0..1, drives the color scale
  hotspots: number;
  owner: string;
};

export function fetchRiskOwnership(
  id: string,
  by: "module" | "file",
): Promise<{ by: string; cells: OwnershipCell[] }> {
  return getJSON<{ by: string; cells: OwnershipCell[] }>(
    `/api/repos/${id}/risk/ownership?by=${by}`,
  );
}

export type ContribNode = { name: string; files: number; commits: number };
export type ContribEdge = { source: string; target: string; weight: number };

export function fetchRiskContributors(
  id: string,
): Promise<{ nodes: ContribNode[]; edges: ContribEdge[] }> {
  return getJSON<{ nodes: ContribNode[]; edges: ContribEdge[] }>(
    `/api/repos/${id}/risk/contributors`,
  );
}

export type SecurityFinding = {
  filePath: string;
  kind: string;
  severity: string;
  snippet: string;
  line: number;
};

export function fetchSecurity(id: string): Promise<SecurityFinding[]> {
  return getJSON<{ findings: SecurityFinding[] }>(`/api/repos/${id}/security`).then(
    (r) => r.findings ?? [],
  );
}

export type CommitCategories = {
  categories: { category: string; count: number }[];
  total: number;
};

export function fetchCommitCategories(id: string): Promise<CommitCategories> {
  return getJSON<CommitCategories>(`/api/repos/${id}/commit-categories`);
}

export type DependencyMatrix = { modules: string[]; cells: number[][] };

export function fetchDependencyMatrix(id: string): Promise<DependencyMatrix> {
  return getJSON<DependencyMatrix>(`/api/repos/${id}/dependency-matrix`);
}

export type Module = {
  name: string;
  files: number;
  symbols: number;
  docsCoverage: number;
  deps: number;
};

export function fetchModules(id: string): Promise<Module[]> {
  return getJSON<{ modules: Module[] }>(`/api/repos/${id}/modules`).then((r) => r.modules ?? []);
}

export type Packages = {
  packages: { name: string; path: string; language: string }[];
  monorepo: boolean;
};

export function fetchPackages(id: string): Promise<Packages> {
  return getJSON<Packages>(`/api/repos/${id}/packages`);
}

export type FlowNode = { node: string; children?: FlowNode[] };

export function fetchExecutionFlows(id: string): Promise<FlowNode[]> {
  return getJSON<{ flows: FlowNode[] }>(`/api/repos/${id}/execution-flows`).then(
    (r) => r.flows ?? [],
  );
}

// ---- per-repo drill-down ------------------------------------------------

export type RepoDetail = {
  id: string;
  slug: string;
  name: string;
  url: string;
  localPath: string;
  defaultBranch: string;
  headCommit: string;
  createdAt: string;
  updatedAt: string;
};

export function fetchRepo(id: string): Promise<RepoDetail> {
  return getJSON<RepoDetail>(`/api/repos/${id}`);
}

export function registerRepo(path: string, ephemeral = false): Promise<RepoDetail> {
  return send<RepoDetail>("POST", "/api/repos/register", { path, ephemeral });
}

export function deleteRepo(id: string): Promise<void> {
  return send<void>("DELETE", `/api/repos/${id}`);
}

// ---- per-repo settings --------------------------------------------------

export type HealthRule = {
  pattern?: string;
  path?: string;
  overrides: Record<string, string>;
};

// ModelCatalog maps a provider/embedder name to its default model and the full
// list of selectable models. A provider may be absent (no enumerable catalog),
// in which case the UI falls back to a free-text model input.
export type ModelCatalog = Record<string, { default: string; models: string[] }>;

// OptionStatus describes whether a provider/embedder is usable in the daemon's
// current environment. `requires` is a human label of the env var needed to
// make it ready ("" when nothing is required, e.g. ollama).
export type OptionStatus = { name: string; ready: boolean; requires: string };

export type RepoSettings = {
  effective: {
    provider: string;
    model: string;
    embedder: string;
    embedding_model: string;
    reasoning: boolean;
    health_rules: HealthRule[] | null;
    [k: string]: unknown;
  };
  overridden: Record<string, boolean>;
  biomarkers: string[];
  global: { providerConfigured: boolean; embedderConfigured: boolean };
  providerOptions: string[];
  embedderOptions: string[];
  providerStatus: OptionStatus[];
  embedderStatus: OptionStatus[];
  providerCatalog: ModelCatalog;
  embedderCatalog: ModelCatalog;
};

export function fetchRepoSettings(id: string): Promise<RepoSettings> {
  return getJSON<RepoSettings>(`/api/repos/${id}/settings`);
}

export function putRepoSetting(id: string, key: string, value: unknown): Promise<void> {
  return send<void>("PUT", `/api/repos/${id}/settings/${key}`, value);
}

export function clearRepoSetting(id: string, key: string): Promise<void> {
  return send<void>("DELETE", `/api/repos/${id}/settings/${key}`);
}

// ---- global settings ----------------------------------------------------

export type GlobalSettings = {
  effective: {
    provider: string;
    model: string;
    embedder: string;
    embedding_model: string;
    [k: string]: unknown;
  };
  overridden: Record<string, boolean>;
  providerOptions: string[];
  embedderOptions: string[];
  providerStatus: OptionStatus[];
  embedderStatus: OptionStatus[];
  providerKeys: Record<string, boolean>;
  global: { providerConfigured: boolean; embedderConfigured: boolean };
  providerCatalog: ModelCatalog;
  embedderCatalog: ModelCatalog;
};

export function fetchGlobalSettings(): Promise<GlobalSettings> {
  return getJSON<GlobalSettings>("/api/settings");
}

export function putGlobalSetting(key: string, value: unknown): Promise<void> {
  return send<void>("PUT", `/api/settings/${key}`, value);
}

export function clearGlobalSetting(key: string): Promise<void> {
  return send<void>("DELETE", `/api/settings/${key}`);
}

// ---- per-repo tasks -----------------------------------------------------

export type TaskInfo = {
  id: string;
  name: string;
  description: string;
  default: boolean;
  enabled: boolean;
  overridden: boolean;
  requires: string[];
};

export type TasksResponse = {
  tasks: TaskInfo[];
  providerConfigured: boolean;
  embedderConfigured: boolean;
};

export function fetchTasks(id: string): Promise<TasksResponse> {
  return getJSON<TasksResponse>(`/api/repos/${id}/tasks`);
}

export function setTask(
  id: string,
  taskId: string,
  enabled: boolean,
): Promise<{ id: string; enabled: boolean }> {
  return send<{ id: string; enabled: boolean }>("PUT", `/api/repos/${id}/tasks/${taskId}`, {
    enabled,
  });
}

export type HealthSummary = {
  averageScore: number;
  findingCount: number;
  findingsByBiomarker: Record<string, number>;
};

export function fetchHealthSummary(id: string): Promise<HealthSummary> {
  return getJSON<HealthSummary>(`/api/repos/${id}/health`);
}

export type HealthFinding = {
  filePath: string;
  biomarkerType: string;
  severity: string;
  functionName?: string;
  lineStart?: number;
  lineEnd?: number;
  healthImpact: number;
  reason: string;
};

export function fetchHealthFindings(id: string): Promise<HealthFinding[]> {
  return getJSON<{ findings: HealthFinding[] }>(
    `/api/repos/${id}/health/findings?limit=200`,
  ).then((r) => r.findings);
}

export type HealthSnapshot = {
  takenAt: string; // RFC3339
  commit: string;
  branch?: string;
  average: number;
  hotspot: number;
  worstPath?: string;
  worst: number;
};

export function fetchHealthHistory(id: string): Promise<HealthSnapshot[]> {
  return getJSON<{ snapshots: HealthSnapshot[] }>(`/api/repos/${id}/health/history`).then(
    (r) => r.snapshots ?? [],
  );
}

export type FileDelta = { path: string; before: number; after: number; delta: number };

export type HealthDiff = {
  hasBaseline: boolean;
  baselineCommit: string;
  baselineBranch: string;
  baselineAvg: number;
  currentAvg: number;
  delta: number;
  regressions: FileDelta[];
  improvements: FileDelta[];
  newFiles: string[];
  removedFiles: string[];
};

export function fetchHealthDiff(id: string): Promise<HealthDiff> {
  return getJSON<HealthDiff>(`/api/repos/${id}/health/diff`);
}

export type Hotspot = {
  path: string;
  churnPercentile: number;
  commitCountTotal: number;
  commitCount90d: number;
  primaryOwner?: string;
  busFactor: number;
  contributorCount: number;
  linesAdded90d: number;
  linesDeleted90d: number;
};

export function fetchHotspots(id: string): Promise<Hotspot[]> {
  return getJSON<{ hotspots: Hotspot[] }>(`/api/repos/${id}/hotspots?limit=50`).then(
    (r) => r.hotspots,
  );
}

export type Decision = {
  title: string;
  source: string;
  evidenceFile?: string;
  evidenceLine?: number;
  confidence: number;
  verification: string;
  decision?: string;
  rationale?: string;
  sourceQuote?: string;
};

export function fetchDecisions(id: string): Promise<Decision[]> {
  return getJSON<{ decisions: Decision[] }>(`/api/repos/${id}/decisions?limit=100`).then(
    (r) => r.decisions,
  );
}

export type DeadCode = {
  kind: string;
  filePath: string;
  symbolName?: string;
  symbolKind?: string;
  confidence: number;
  reason: string;
  safeToDelete: boolean;
};

export function fetchDeadCode(id: string): Promise<DeadCode[]> {
  return getJSON<{ findings: DeadCode[] }>(`/api/repos/${id}/dead-code?limit=200`).then(
    (r) => r.findings,
  );
}

// ---- wiki (generated pages) --------------------------------------------

export type WikiPage = {
  id: string;
  pageType: string;
  targetPath: string;
  title: string;
  summary?: string;
  version: number;
  freshness: string;
  modelName: string;
  providerName: string;
  updatedAt: string;
  inputTokens?: number;
  outputTokens?: number;
};

export function fetchPages(id: string): Promise<WikiPage[]> {
  return getJSON<{ pages: WikiPage[] }>(`/api/repos/${id}/pages?limit=500`).then((r) => r.pages);
}

export type WikiPageContent = WikiPage & { content: string; sourceHash: string };

export function fetchPage(id: string, targetPath: string, kind: string): Promise<WikiPageContent> {
  return getJSON<WikiPageContent>(
    `/api/repos/${id}/pages/show?path=${encodeURIComponent(targetPath)}&kind=${encodeURIComponent(kind)}`,
  );
}

// ---- search -------------------------------------------------------------

export type SearchHit = {
  nodeId: string;
  nodeType: string;
  kind?: string;
  name?: string;
  filePath?: string;
  startLine?: number;
  pagerank: number;
};

export function searchSymbols(id: string, q: string, type?: string): Promise<SearchHit[]> {
  const params = new URLSearchParams({ q, limit: "100" });
  if (type) params.set("type", type);
  return getJSON<{ matches: SearchHit[] }>(`/api/repos/${id}/search?${params}`).then(
    (r) => r.matches,
  );
}

// ---- graph / symbols ----------------------------------------------------

export type GraphNode = {
  nodeId: string;
  nodeType: string;
  language?: string;
  symbolCount?: number;
  pagerank?: number;
  betweenness?: number;
  communityId?: number;
  kind?: string;
  name?: string;
  filePath?: string;
  startLine?: number;
  endLine?: number;
  visibility?: string;
};

export function fetchGraphNodes(id: string, type?: string, limit = 100): Promise<GraphNode[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (type) params.set("type", type);
  return getJSON<{ nodes: GraphNode[] }>(`/api/repos/${id}/graph/nodes?${params}`).then(
    (r) => r.nodes,
  );
}

export type GraphEdge = {
  source: string;
  target: string;
  edgeType: string;
  confidence: number;
  importedNames?: string[];
};

export function fetchGraphEdges(id: string, type?: string, limit = 200): Promise<GraphEdge[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (type) params.set("type", type);
  return getJSON<{ edges: GraphEdge[] }>(`/api/repos/${id}/graph/edges?${params}`).then(
    (r) => r.edges,
  );
}
