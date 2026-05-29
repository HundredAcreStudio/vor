// Thin client over the vor daemon's REST API. In dev, Vite proxies /api to
// the daemon; in production the daemon serves this bundle, so relative URLs
// resolve to the same origin.

export type RepoSummary = {
  id: string;
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

// ---- per-repo drill-down ------------------------------------------------

export type RepoDetail = {
  id: string;
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

export type RepoSettings = {
  effective: {
    provider: string;
    model: string;
    reasoning: boolean;
    health_rules: HealthRule[] | null;
    [k: string]: unknown;
  };
  overridden: Record<string, boolean>;
  biomarkers: string[];
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
};

export function fetchPages(id: string): Promise<WikiPage[]> {
  return getJSON<{ pages: WikiPage[] }>(`/api/repos/${id}/pages?limit=500`).then((r) => r.pages);
}

export type WikiPageContent = WikiPage & { content: string; sourceHash: string };

export function fetchPage(id: string, targetPath: string): Promise<WikiPageContent> {
  return getJSON<WikiPageContent>(
    `/api/repos/${id}/pages/show?path=${encodeURIComponent(targetPath)}`,
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
