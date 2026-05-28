# repowise-go — Post-Parity Roadmap

Phases 0–11 of [PORTING_PLAN.md](PORTING_PLAN.md) are complete: the Go
port reaches functional parity with the Python project's core, server,
and CLI. This document tracks the work **beyond** parity — the pieces
the original plan listed as aspirational (real LLM providers, the full
14-language set, deeper biomarkers, incremental indexing, webhooks) plus
the gaps surfaced while porting.

It is the source of truth for what's left. Each phase ends with a
working binary and tests, same as the original plan.

> Status legend: ⬜ not started · 🟡 partial · ✅ done

---

## Where we are (end of Phase 11)

| Area | Shipped | Known gap |
|---|---|---|
| Persistence | SQLite + Postgres, goose, 29 tables, FTS, vector store | pgvector/LanceDB native index |
| Ingestion | 14 parsers, graph + metrics, externals (5 ecosystems + OpenAPI/gRPC/GraphQL contracts) | — (Phase 15 ✅) |
| Git intelligence | hotspots, ownership, co-change, bus factor | — |
| Providers | Mock + Anthropic + OpenAI/Gemini/Ollama/LiteLLM, cost/retry/ratelimit middleware | — (Phase 12 ✅) |
| Generation | pages (file/dir/symbol), context (RAG), templates, resume | architecture page kind |
| Analysis | 11 biomarkers, dead code, 4-source decisions, Louvain communities, coverage ingest, security scan | ~6 more biomarkers; Leiden; trends/governance (Phase 14 ✅) |
| Pipeline | init/update (incremental parse cache), phase timing, checkpoint/resume | graph/git still rebuilt fully (Phase 15 ✅ for parse) |
| Server | HTTP `/api`, MCP (22 tools, stdio + Streamable HTTP) | webhooks, scheduler |
| CLI | full command set, `--repo`/`--repo-id`, semantic search | charmbracelet TUI, editor MCP registration |
| Workspace | multi-repo registry, cross-repo co-change, `serve --auto` | — |
| Search | substring + embedding-backed semantic (mock embedder) | real embedder for meaningful ranking |

---

## ✅ Phase 12 — Production LLM providers & embedders (done)

All providers speak their vendor API directly over net/http (no SDK
dependency), matching the existing Anthropic implementation.

- ✅ OpenAI provider — `/v1/chat/completions` Generate + SSE
  GenerateStream; exported `NewCompatible` for reuse.
- ✅ Google Gemini provider — `:generateContent` + `:streamGenerateContent`
  (SSE), `x-goog-api-key` auth, `model` role mapping.
- ✅ Ollama provider — local `/api/chat` (NDJSON stream), no key, free.
- ✅ LiteLLM — thin OpenAI-compatible passthrough (own name for cost
  attribution, `base_url` required).
- ✅ Real embedders: OpenAI `/v1/embeddings` (text-embedding-3-* with
  Matryoshka `dimensions`), Gemini `:batchEmbedContents`, Ollama
  `/api/embed` — all registered under their provider name.
- ✅ Cost catalog updated (incl. fixed dotted Gemini ids) so generation
  through any provider records to `llm_costs` via the existing middleware.
- ✅ CLI wiring: `--provider openai|google|ollama|litellm` and
  `config.embedder` resolve keys/base-URLs from env
  (`OPENAI_API_KEY`, `GEMINI_API_KEY`/`GOOGLE_API_KEY`,
  `REPOWISE_OLLAMA_BASE_URL`, `REPOWISE_LITELLM_BASE_URL`).

All four providers + embedders are covered by httptest-based unit tests.
The remaining caveat is empirical: meaningful semantic-search ranking
needs a real embedder key at runtime (the mock stays the zero-config
default).

## ✅ Phase 13 — Language coverage (→ 14 languages) (done)

Added six parsers, taking the set from 8 to 14 (Go, Python, JS, TS,
Java, C#, C, C++, Rust + **Ruby, PHP, Swift, Kotlin, Scala, Lua/Luau**).

- ✅ tree-sitter grammars bound (smacker bindings: ruby, php, swift,
  kotlin, scala, lua — Luau rides the Lua grammar as a superset).
- ✅ Embedded `*.scm` query files for symbol + import + call extraction.
- ✅ A shared `common.GenericExtract` drives all six (Partial/Traversal
  tier) so each parser package is a thin grammar + kind-map shim; the
  AST node-type names were derived empirically, not guessed.
- ✅ Registered in the `init`/`ingest` side-effect import blocks.
- ✅ Verified end-to-end: a polyglot fixture indexes file + symbol nodes
  for every new language; unit tests assert symbol/import/call
  extraction + kind detection (swift struct/protocol, scala trait, and
  method re-tagging inside classes).

Deferred (not blocking): per-language call resolvers — the three-tier
resolver's common fallback handles these for now; bespoke resolvers can
land if cross-file resolution quality demands it.

## ✅ Phase 14 — Code-health & graph-analysis depth (done)

- ✅ **Louvain community detection** (gonum `community.Modularize` over an
  undirected weighted view, deterministic seed) replaces the
  connected-component placeholder in `graph/metrics.go`. Verified it
  splits bridged clusters that the old labeller would have merged.
- ✅ **Coverage ingest**: an LCOV + Cobertura parser
  (`analysis/health/coverage`) → `coveragestore` → `repowise coverage
  import|status`. When present, coverage makes `untested_hotspot`
  authoritative (below-threshold line coverage = untested), with impact
  scaling as coverage drops.
- ✅ **Three new biomarkers** (8 → 11): `long_parameter_list`
  (primitive-obsession proxy from signatures), `feature_envy` (a method
  leaning on one external receiver more than its own class), and
  `shotgun_surgery` (a file co-changing with many others).
- ✅ **Security scanner** (`analysis/security`): pattern-based detection
  of hardcoded secrets (redacted), private keys, weak crypto, and
  concatenation-driven SQL/command-injection sinks → `securitystore` →
  `repowise security scan|list` + the `repowise_security` MCP tool.

Deferred (not blocking): the remaining ~6 biomarkers (congestion,
knowledge loss, code-age volatility, …), Leiden, and health
trends/governance depth — the framework is in place to add them.

## ✅ Phase 15 — Incremental indexing & API-contract extraction (done)

- ✅ **Incremental parse**: a per-file `parse_cache` (content hash +
  `parserVersion`) backed by `parsestore`. The pipeline parse phase
  reuses cached results for unchanged files, re-parses only changed/new
  ones, and prunes deleted files — skipping the dominant (cgo
  tree-sitter) cost on `update`. Run `Result.ParseStats` reports
  parsed/reused/pruned; verified: edit one file → exactly one re-parse,
  delete one → one prune, unchanged re-index → zero parses. A
  `parserVersion` bump transparently invalidates the whole cache.
- ✅ **API-contract extraction** via three new external extractors fed
  through the existing externals phase: OpenAPI/Swagger (YAML+JSON) →
  `METHOD /path` endpoints, protobuf → gRPC services + `Service.Rpc`
  methods, GraphQL SDL → type/input/enum/interface definitions. They
  land in `external_systems` (ecosystems openapi/grpc/graphql).

`repowise serve` now also prints copy-paste MCP client-install
instructions (Claude Code + generic `mcp.json`) at startup.

Deferred (not blocking): graph + git intelligence still run fully each
re-index (incremental there is a separate, harder problem); Dockerfile /
CI build-step extraction.

## Phase 16 — Risk & knowledge intelligence

- PR blast-radius / `get_risk` PR-mode: a `directive` block that, given
  a changeset, returns impacted files, owners, hotspots, and review
  guidance (the one MCP gap remaining vs. Python's `get_risk`).
- Knowledge-graph depth: richer decision ↔ code linking
  (`decision_node_links`) and a knowledge-map view.
- Architecture **page kind** for generation (whole-system narrative),
  alongside the existing file/directory/symbol pages.

**Acceptance:** `repowise_get_risk` (PR mode) returns a directive block
for a diff; an architecture page generates and is searchable;
decision-to-symbol links populate.

## Phase 17 — Server completeness & client UX

- ✅ **MCP mutation tools** (single-user local): `repowise_reindex`
  (async — returns a run_id, runs the pipeline in the background, guards
  against double-firing an in-progress run; poll `repowise_pipeline_log`)
  and `repowise_security_scan` (sync). Lets an attached agent refresh the
  index itself instead of shelling out. Left ungated since the target is
  a single-user local daemon; revisit `--allow-write` if it ever serves
  multiple clients.
- ✅ `repowise serve` prints MCP client-install instructions at startup.
- GitHub + GitLab **webhooks**: auto re-index on push, signature
  verification, background job dispatch.
- **Scheduler** (`robfig/cron`): periodic maintenance (re-index drift,
  cost rollups, stale-page detection).
- Background job executor with status surfaced over `/api` and MCP.
- charmbracelet **TUI** for interactive `status` / `health` views.
- Editor MCP-registration helpers (Claude Code / Cursor) so
  `repowise mcp install` wires the daemon into a client config.
- Vector scaling: pgvector native index on Postgres + optional LanceDB
  adapter; embed **symbols** as well as pages.

**Acceptance:** a push webhook triggers a re-index; the scheduler runs a
maintenance job; `repowise mcp install` writes a valid client config;
semantic search covers symbols.

---

## Sequencing notes

- **12 first** — real providers/embedders unlock meaningful output for
  everything downstream (generation quality, semantic search ranking).
- **13 and 14 are independent** and can run in parallel.
- **15 (incremental)** is the biggest UX/perf win on large repos; do it
  before webhooks (17) so auto-re-index is cheap.
- **16 and 17** are the "rich product" layer — valuable but not
  blocking core intelligence.

Non-goals stay as in PORTING_PLAN §7 (no runtime plugins, no reading
Python `.repowise/wiki.db`, dashboard lives elsewhere).
