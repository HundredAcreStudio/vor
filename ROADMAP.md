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
| Ingestion | 8 parsers, graph + metrics, externals (5 ecosystems) | 6 more languages; API-contract extraction |
| Git intelligence | hotspots, ownership, co-change, bus factor | — |
| Providers | Mock + Anthropic + OpenAI/Gemini/Ollama/LiteLLM, cost/retry/ratelimit middleware | — (Phase 12 ✅) |
| Generation | pages (file/dir/symbol), context (RAG), templates, resume | architecture page kind |
| Analysis | 8 biomarkers, dead code, 4-source decisions | 9 more biomarkers; coverage ingest; Louvain/Leiden; security scan |
| Pipeline | init/update, phase timing, checkpoint/resume | true incremental (update == full re-index today) |
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

## Phase 13 — Language coverage (→ 14 languages)

Eight parsers exist (Go, Python, JS, TS, Java, C#, C++, Rust). The plan
targets 14.

- Add tree-sitter grammars + parsers for **Ruby, PHP, Swift, Kotlin,
  Scala, Luau**.
- Per-language `*.scm` query files (`go:embed`) for symbol + import +
  call extraction.
- Call resolvers registered per language where the three-tier resolver
  needs language-specific rules.
- Parser parity tests against fixture projects under `testdata/fixtures`.

**Acceptance:** each new language ingests a fixture repo into
`graph_nodes`/`graph_edges`; `repowise status` reports the language;
resolver tests pass.

## Phase 14 — Code-health & graph-analysis depth

Eight biomarkers ship (brain_method, deep_nesting, high_complexity,
long_function, duplication, hidden_coupling, god_class,
untested_hotspot). Python has ~17, and community detection is currently
just connected-components labelling.

- Remaining biomarkers: primitive obsession, congestion, knowledge
  loss, blame-based function hotspots, code-age volatility, feature
  envy, shotgun surgery, plus the rest to reach Python's set.
- Coverage ingest (LCOV / Cobertura / Clover) so `untested_hotspot` is
  driven by real coverage, not heuristics.
- Health trends + governance flags + refactoring-impact scoring depth.
- Real **Louvain** community detection (gonum), replacing the
  connected-component placeholder in `graph/metrics.go`; evaluate
  Leiden if quality demands it.
- Security vulnerability scanner populating `security_findings`.

**Acceptance:** `repowise health` reports the expanded biomarker set;
coverage files are ingested and reflected in scores; `community_id` on
graph nodes comes from modularity-based detection;
`repowise_get_community` reflects real communities.

## Phase 15 — Incremental indexing & API-contract extraction

`update` currently re-runs the full pipeline (`ModeUpdate` is a no-op
vs. `init`). Real incremental indexing is the biggest single perf win.

- `ChangeDetector`: diff content hashes against persisted state, parse
  and re-graph only changed files, prune deleted ones.
- Wire it into the pipeline so `update` is genuinely incremental and
  `watch` stays cheap on large repos.
- API-contract **extraction** (not just filename detection): pull HTTP
  routes from OpenAPI/Swagger, services/messages from protobuf, types
  from GraphQL SDL, build steps from Dockerfiles/CI into
  `external_systems`.

**Acceptance:** editing one file re-indexes only that file (assert via
pipeline log / timing); OpenAPI + proto fixtures produce extracted
endpoints/services in the graph.

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
