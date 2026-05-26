# repowise-go — Porting Plan

This document captures the design decisions and phased roadmap for porting [repowise](https://github.com/repowise-dev/repowise) from Python to Go. The Go port targets **full feature parity** with the Python implementation across `packages/core`, `packages/server`, and `packages/cli`. The Next.js dashboard is out of scope for this repository — it will live in a separate `repowise-dashboard` repo and communicate with this backend over HTTP.

Source reference: `~/projects/repowise` (Python 3.11+, v0.12.0).

---

## 1. Scope

| Python package | Go port | Status |
|---|---|---|
| `packages/core` | `internal/ingestion`, `internal/analysis`, `internal/generation`, `internal/persistence`, `internal/providers`, `internal/pipeline`, `internal/workspace` | In scope |
| `packages/server` | `internal/server` (`http/`, `mcp/`, `services/`, `schemas/`) | In scope |
| `packages/cli` | `internal/cli`, `cmd/repowise`, `cmd/repowise-augment` | In scope |
| `packages/types` | N/A — Go has its own types in `internal/...` | Out of scope |
| `packages/ui` + `packages/web` | Separate `repowise-dashboard` repo (Next.js) | Out of scope |

---

## 2. Module Layout

```
repowise-port/
├── go.mod
├── go.sum
├── Makefile
├── README.md
├── PORTING_PLAN.md
├── cmd/
│   ├── repowise/                 # main CLI binary
│   └── repowise-augment/         # Claude Code Grep/Glob enrichment hook
├── internal/
│   ├── config/                   # .repowise/config.yaml + env loading
│   ├── logging/                  # slog setup + helpers
│   ├── version/                  # ldflags-injected version info
│   │
│   ├── persistence/              # SQL + vector + search
│   │   ├── db/                   # connection, pool, sqlite/pgx
│   │   ├── migrations/           # goose .sql files (embedded)
│   │   ├── queries/              # sqlc-generated query code
│   │   ├── models/               # Go structs mirroring tables
│   │   ├── search/               # FTS5 + tsvector wrappers
│   │   └── vector/               # VectorStore interface + impls
│   │
│   ├── ingestion/
│   │   ├── traverser/            # FileTraverser
│   │   ├── parser/               # ASTParser (tree-sitter)
│   │   ├── languages/            # 14-language registry
│   │   ├── queries/              # *.scm files (go:embed)
│   │   ├── graph/                # GraphBuilder, metrics, resolvers
│   │   ├── git/                  # GitIndexer
│   │   ├── change/               # ChangeDetector (incremental)
│   │   ├── external/             # npm/pip/cargo/go.mod/nuget extractors
│   │   ├── handlers/             # OpenAPI, protobuf, GraphQL, Dockerfile, CI
│   │   └── models/               # Symbol, ParsedFile, Import, CallSite
│   │
│   ├── analysis/
│   │   ├── deadcode/             # UnreachableCodeDetector
│   │   ├── health/
│   │   │   ├── biomarkers/       # 15 biomarker detectors
│   │   │   ├── complexity/       # McCabe + nesting
│   │   │   ├── coverage/         # LCOV/Cobertura/Clover ingest
│   │   │   ├── duplication/      # Rabin-Karp clone detection
│   │   │   ├── engine.go         # CodeHealthEngine
│   │   │   ├── governance.go
│   │   │   ├── scoring.go
│   │   │   ├── suggestions.go
│   │   │   └── trends.go
│   │   ├── decisions/            # DecisionExtractor (8 sources) + evolution
│   │   ├── community/            # Louvain → Leiden
│   │   ├── flows/                # ExecutionFlowAnalyzer
│   │   ├── security/             # vulnerability patterns
│   │   ├── knowledge/            # decision ↔ code linking
│   │   └── prblast/              # PR blast radius
│   │
│   ├── generation/
│   │   ├── pages/                # HierarchicalPageGenerator
│   │   ├── context/              # ContextAssembler (RAG)
│   │   ├── templates/            # Jinja2 → text/template, go:embed
│   │   ├── jobs/                 # PipelineJob state machine
│   │   ├── editor/               # CLAUDE.md / cursor.md generators
│   │   └── models/               # GeneratedPage, GenerationConfig
│   │
│   ├── providers/                # LLM abstraction
│   │   ├── anthropic/
│   │   ├── openai/
│   │   ├── google/               # Gemini
│   │   ├── ollama/
│   │   ├── litellm/              # router (HTTP)
│   │   ├── mock/
│   │   ├── embedding/            # Embedder interface + impls
│   │   ├── ratelimit/            # token-bucket RPM + TPM
│   │   └── registry.go
│   │
│   ├── pipeline/                 # orchestrator + checkpoint
│   ├── workspace/                # multi-repo aggregation
│   │
│   ├── server/
│   │   ├── http/                 # chi routes
│   │   │   ├── routes/           # 20+ route packages
│   │   │   ├── middleware/
│   │   │   └── server.go
│   │   ├── mcp/                  # 17 MCP tools
│   │   ├── services/             # business logic (C4, owners, etc.)
│   │   ├── schemas/              # response DTOs
│   │   ├── scheduler/            # cron jobs
│   │   └── webhooks/             # GitHub/GitLab handlers
│   │
│   └── cli/
│       ├── commands/             # 14 subcommands
│       ├── ui/                   # charmbracelet UI helpers
│       ├── hooks/                # git post-commit hook installer
│       ├── editor/               # Claude Code / Cursor integration
│       └── costs/                # cost estimator
│
├── migrations/                   # goose SQL files (referenced by embed)
├── docs/                         # ported user docs
└── testdata/                     # fixture repos for parity tests
```

**Module path:** `github.com/repowise-dev/repowise-go` (placeholder — will adjust if upstream wants a different name).

**Go version:** 1.24+ (matches `go version` on dev machine).

**Visibility:** Everything under `internal/`. We do not commit to a public Go API surface yet — the CLI and HTTP/MCP servers are the only consumers. If a plugin surface is needed later, promote types to `pkg/repowise/`.

---

## 3. Library Choices

Decisions here are deliberate and load-bearing — changing them later costs a lot. Each pick is paired with the Python equivalent it replaces.

| Concern | Python | Go choice | Rationale |
|---|---|---|---|
| HTTP router | FastAPI | `github.com/go-chi/chi/v5` | Stdlib-compatible, minimal, mature. Avoids gin's middleware surface. |
| Server | uvicorn | stdlib `net/http` | No separate ASGI runner needed. |
| ORM / queries | SQLAlchemy 2.0 async | `sqlc` + `database/sql` | Compile-time SQL; avoids ORM impedance. Plus hand-written queries where dialect-specific. |
| SQLite driver | aiosqlite | `modernc.org/sqlite` | Pure Go, no cgo. Lighter footprint than `mattn/go-sqlite3`. |
| Postgres driver | asyncpg | `github.com/jackc/pgx/v5` | The canonical Go pgx, includes pgvector support via extension. |
| Migrations | Alembic | `github.com/pressly/goose/v3` | Embeddable, supports both SQLite and Postgres. |
| Tree-sitter | `tree-sitter` + 14 wheels | `github.com/smacker/go-tree-sitter` | The most complete Go binding; cgo, but unavoidable. |
| Git operations | gitpython | `github.com/go-git/go-git/v5` | Pure Go, no cgo, sufficient for our read-only history queries. |
| Graph algorithms | networkx + scipy | `gonum.org/v1/gonum/graph` | PageRank, SCC, betweenness, connected components. |
| Community detection | graspologic (Leiden) | `gonum.org/v1/gonum/graph/community` (Louvain) + custom Leiden later | Louvain is in gonum; Leiden has no canonical Go port — we'll port one in Phase 6. |
| LLM SDK — Anthropic | `anthropic` | `github.com/anthropics/anthropic-sdk-go` | Official. Includes prompt caching, batch API. |
| LLM SDK — OpenAI | `openai` | `github.com/openai/openai-go` | Official. |
| LLM SDK — Gemini | `google-genai` | `google.golang.org/genai` | Official. |
| LLM SDK — Ollama | (HTTP) | `github.com/ollama/ollama/api` | Vendor-supplied client. |
| LiteLLM | `litellm` | direct HTTP to litellm proxy | LiteLLM is a Python lib; in Go we talk to its OpenAI-compatible proxy. |
| MCP SDK | `mcp` | `github.com/mark3labs/mcp-go` | Most active community Go MCP SDK. |
| CLI framework | click | `github.com/spf13/cobra` | The Go standard for nested commands. |
| Terminal UI | rich | `github.com/charmbracelet/lipgloss` + `bubbles` + `bubbletea` for interactive | Best-in-class TUI in Go. |
| Templates | Jinja2 | `text/template` (stdlib) | Sufficient for prompt assembly; we'll write helpers for missing features. |
| YAML | PyYAML | `gopkg.in/yaml.v3` | Standard. |
| Gitignore | pathspec | `github.com/sabhiram/go-gitignore` | Maintained, follows git semantics. |
| File watcher | watchdog | `github.com/fsnotify/fsnotify` | Cross-platform standard. |
| Logging | structlog | `log/slog` (stdlib) | Structured logging in stdlib since 1.21. |
| Validation | Pydantic | `github.com/go-playground/validator/v10` | Tag-based; pairs with JSON unmarshaling. |
| Retry | tenacity | `github.com/cenkalti/backoff/v4` | Exponential backoff, mature. |
| Rate limit | custom token bucket | `golang.org/x/time/rate` | RPM bucket. TPM we wrap by adapting `rate.Limiter` to weighted Wait. |
| Cron / scheduler | APScheduler | `github.com/robfig/cron/v3` | Lightweight, fits webhook + maintenance jobs. |
| Vector store — local | LanceDB | `github.com/lancedb/lancedb` Go SDK if mature; else HTTP via REST | First impl is brute-force over SQLite; LanceDB adapter in Phase 6. |
| Vector store — Postgres | pgvector | `pgvector-go` (via pgx) | Same as Python. |

---

## 4. Database Schema

The Python project has 24 Alembic migrations evolving the schema over time. For the Go port we **do not replay migration history**. We collapse the end state into a single consolidated `0001_init.sql` migration. Rationale:

- Go and Python wiki.db files are not interchangeable anyway (different JSON encoding choices, different vector-store layout, no plan to support reading an existing Python `.repowise/wiki.db`).
- Replaying 24 migrations on first install wastes time and ties us to Python's incremental story.
- Future Go-side schema changes are added as `0002_*.sql`, `0003_*.sql`, etc., with goose.

The 24 Python migrations are kept as design reference under `docs/python-migrations-reference.md` (added in Phase 1).

Tables in consolidated schema (mirroring the Python end state):

`repositories`, `wiki_pages`, `wiki_page_versions`, `generation_jobs`, `pipeline_jobs`, `graph_nodes`, `graph_edges`, `graph_metrics`, `git_metadata`, `external_systems`, `community_metadata`, `dead_code_findings`, `code_health_findings`, `decision_records`, `decision_evidence`, `decision_edges` (the decision graph), `knowledge_graph`, `chat_conversations`, `llm_costs`, `security_findings`, `answer_cache`, `page_summaries` — plus FTS5 virtual tables for full-text search on SQLite, and `tsvector` columns + GIN indexes on Postgres.

---

## 5. Phased Roadmap

Phases are sized for small, testable increments. Each phase ends with a working binary (or set of binaries) and tests.

### Phase 0 — Foundation
Module init, layout, Makefile, `.gitignore`. Config loading (`.repowise/config.yaml` + env vars). Structured logging via `log/slog` with a development-friendly handler. Version package. Error types. Test scaffolding.
**Deliverable:** `go build ./...` succeeds; `repowise version` prints version info.

### Phase 1 — Persistence
Consolidated `0001_init.sql` schema. Goose migration runner. sqlc query generation for both dialects. CRUD covering every table. FTS5 + tsvector search wrappers. `VectorStore` interface with `Mock` and `SQLiteBruteForce` backends; LanceDB/pgvector adapters stubbed and marked TODO.
**Deliverable:** `repowise db migrate` creates schema; round-trip tests pass for every table.

### Phase 2 — Ingestion
`FileTraverser` with gitignore + `.repowiseIgnore`. `ASTParser` over `smacker/go-tree-sitter` for the 14 languages. Tree-sitter query files embedded via `go:embed`. `GraphBuilder` produces file + symbol nodes and four edge types over gonum. Three-tier call resolver. Heritage extractor. External-systems extractors (npm/pip/cargo/go.mod/nuget). Special handlers (OpenAPI/proto/GraphQL/Dockerfile/CI). Graph metrics (PageRank, SCC, betweenness).
**Deliverable:** `repowise ingest <path>` writes graph_nodes/edges/metrics to DB; benchmarks comparable to Python.

### Phase 3 — Git intelligence
`go-git` integration. `GitIndexer` produces hotspots, ownership %, co-change pairs, bus factor, contributor profiles. Rename + merge handling. `ChangeDetector` for incremental update path.
**Deliverable:** `repowise git-index <path>` populates `git_metadata`; co-change pairs match Python within tolerance.

### Phase 4 — LLM providers
`Provider` interface with `Generate`, `GenerateStream`, `BatchGenerate`. Registry. Retry + rate limit. Implementations: Anthropic (with prompt caching + batch API), OpenAI, Gemini, Ollama, Mock. `Embedder` interface + Mock/Gemini/OpenAI/OpenRouter. LiteLLM as an HTTP proxy target. LLM cost tracking persistence hook.
**Deliverable:** `repowise smoke-llm --provider=anthropic` round-trips a generation; cost is persisted to `llm_costs`.

### Phase 5 — Generation
`ContextAssembler` for RAG context (wiki + graph + git + source snippets + decisions). `HierarchicalPageGenerator` with checkpoint/resume across the 1–5 levels. Prompt templates via `text/template` + `go:embed`. `JobSystem` state machine. `EditorFileGenerator` with marker-merge for CLAUDE.md / cursor.md.
**Deliverable:** `repowise generate --target file:foo.go` produces a wiki page; resume works after kill.

### Phase 6 — Analysis subsystems
Code health: 15 biomarkers (McCabe, deep nesting, brain methods, Rabin-Karp duplication, untested hotspots, primitive obsession, congestion, knowledge loss, blame-based function hotspots, code-age volatility, plus the five not yet enumerated) + scoring + suggestions + trends + governance flags. Dead code with confidence tiers. Decision extractor (8 sources) + evolution + evidence + graph edges. Louvain community detection (Leiden a stretch). Security scan. Execution flows. PR blast radius.
**Deliverable:** `repowise health`, `repowise dead-code`, `repowise decision list` all work; output schema matches Python.

### Phase 7 — Pipeline
`run_pipeline` orchestrator with `INIT` and `UPDATE` modes. Phase timing. Checkpoint persistence. `pipeline_jobs` state machine. `persist_pipeline_result` fans out to SQL + vector store.
**Deliverable:** `repowise init <path>` and `repowise update <path>` complete end-to-end; checkpoints survive kill.

### Phase 8 — Server (HTTP + MCP)
chi-based HTTP server. 20+ route packages mirroring FastAPI surface. `mark3labs/mcp-go` server with all 17 tools. Service layer (C4 builder, owner profile, reviewer suggestions, knowledge map, module health, symbol ranking). GitHub + GitLab webhooks. cron-based scheduler. Background job executor.
**Deliverable:** `repowise serve` exposes `/api/*` and MCP on stdio; OpenAPI parity with Python (manual diff).

### Phase 9 — CLI
Cobra CLI with 14 commands (`init`, `update`, `watch`, `serve`, `search`, `export`, `status`, `doctor`, `delete`, `health`, `dead-code`, `decision`, `costs`, `augment`, `claude-md`, `reindex`, `hook`, `mcp`, `workspace`). charmbracelet TUI. Editor integrations (Claude Code / Cursor MCP registration). Cost estimator. Git hooks installer.
**Deliverable:** `repowise --help` matches Python output (or improves on it); each subcommand has at least a smoke test.

### Phase 10 — Workspace (multi-repo)
Multi-repo workspace analysis. Cross-repo co-change. API contract extraction (HTTP route, gRPC, message topics). Federated MCP queries.
**Deliverable:** `repowise workspace init` + `repowise workspace status` work over a two-repo fixture.

### Phase 11 — Parity testing & polish
Integration tests on real fixture repos (`testdata/`). Parity tests comparing Go output vs Python for graph, health, decisions, dead-code. Performance benchmarks (target: ≥ Python speed; we expect substantial improvement on cold cache thanks to goroutine fan-out). Docs (README, USER_GUIDE, CLI_REFERENCE).

---

## 6. Risk Areas

1. **Tree-sitter via cgo.** `smacker/go-tree-sitter` requires cgo and pulls in 14 C grammar libraries. Cross-compilation gets harder. Mitigation: provide prebuilt binaries; document cgo requirement clearly; investigate `tree-sitter-go-bindings` purity later.

2. **Leiden community detection.** No canonical Go port. Plan: use Louvain (gonum) for Phase 6; community structure is similar enough that downstream consumers won't notice for v1. Port Leiden later if quality demands it.

3. **LanceDB Go SDK maturity.** SDK exists but is newer than the Python equivalent. Plan: brute-force SQLite-backed vector search first (works fine ≤ 50k vectors), then add LanceDB adapter when stable.

4. **MCP Go SDK maturity.** `mark3labs/mcp-go` is active but moves fast; protocol breaks possible. Mitigation: wrap behind our own `mcp.Tool` interface so the SDK is swappable.

5. **Async → goroutines.** Python's FastAPI dependency injection + async session management has no direct equivalent. The Go version uses explicit `context.Context` plumbing; care needed to make sure long-running operations are cancellable.

6. **Composite string PKs.** Python uses `{page_type}:{target_path}` as a page ID. Go's `sqlc` is fine with TEXT primary keys, but we must be careful with sorting and indexing.

7. **JSON blobs in TEXT columns.** Mirrors Python; risk is field drift between Go and Python serialization. Mitigation: golden tests on serialized output for shared types.

8. **Health biomarker portability.** 15 biomarkers are deterministic and well-specified, but some rely on Python tree-sitter quirks (node names, query captures). Each biomarker needs its own port + parity test.

---

## 7. Non-Goals (v1)

- Plugin loading at runtime (Python's `register_command` will become compile-time interface registration).
- Reading existing Python `.repowise/wiki.db` files (incompatible by design — users must re-index).
- Windows support beyond "best-effort" (Python repowise targets Linux/macOS; Go port follows suit).
- Web dashboard in this repo (handled by `repowise-dashboard`).

---

## 8. Out-of-Band Decisions Locked

- Module path: `github.com/repowise-dev/repowise-go` (may change to vanity import later — internal imports go through Go module path either way).
- Go version: 1.24+.
- License: AGPL-3.0-only, matching upstream.
- No `pkg/` exports until a plugin surface is needed.
- Migrations: single consolidated `0001_init.sql` reflecting Python's end state, no replay.
- CLI: single binary `repowise` + auxiliary `repowise-augment` for the Claude Code hook.

---

## 9. Open Questions

These don't block work — answers will land as the relevant phase begins.

- **Leiden vs Louvain trade-off.** Quantify quality difference on a real repo before deciding whether to port Leiden.
- **Prompt template engine.** `text/template` is sparse compared to Jinja2. Pebble (`flosch/pongo2`) is a closer match but adds a dependency. Defer until Phase 5.
- **MCP transport.** stdio is required for editor integration; SSE for HTTP. Confirm `mark3labs/mcp-go` supports both cleanly.
- **Vector dim mismatch handling.** Python silently re-embeds when dimensions change; Go should surface this loudly. Phase 1 to confirm migration strategy.
