# Parity with the Python implementation

Tracks how the Go port (**vor**) compares to upstream
[repowise](https://github.com/repowise-dev/repowise) (Python, v0.12.x).
Phase 11 reference — updated as gaps close.

Legend: ✅ full parity · 🟡 partial / behavioural difference · ⏳ not yet · ➕ Go-only addition · ⤳ capability relocated (dashboard/daemon, not the CLI)

---

## CLI commands

The Go port deliberately keeps a **lean CLI** and moves browsing into the
[web dashboard](./dashboard.md) and index-freshness into the daemon's
auto-indexer. So many Python CLI commands have no Go CLI equivalent **by
design** — the capability lives elsewhere, not that it's missing.

| Python | Go CLI | Status | Notes |
|---|---|---|---|
| `init` | `init` | ✅ | Go also auto-regenerates CLAUDE.md after init |
| `reindex` | `reindex` | ✅ | Go requires `--yes` (destructive) |
| `delete` | `delete` | ✅ | Go requires `--yes` |
| `serve` | `serve` | ✅ | Go serves dashboard `/` + REST `/api` + MCP `/mcp` on one port |
| `mcp` | `mcp` | ✅ | stdio transport |
| `status` | `status` | ✅ | Go also reports daemon state |
| `doctor` | `doctor` | ✅ | |
| `search` | `search` | 🟡 | LIKE symbol search + `--semantic` page ranking |
| `generate-claude-md` | `claude-md` (alias `generate-claude-md`) | ✅ | byte-identical marker format; now emits proactive MCP-tool directives |
| `augment` | `vor-augment` binary | ✅ | separate binary |
| — | `register` / `unregister` ➕ | ➕ | track/untrack a repo with the daemon (index + watch) |
| — | `daemon` (group) ➕ | ➕ | start/stop/status/restart/logs for the background daemon |
| — | `db` ➕ | ➕ | migration management |
| `update` | — (daemon auto-index) | ⤳ | the daemon watches tracked repos and re-indexes on change |
| `watch` | — (daemon auto-index) | ⤳ | same — no standalone watcher command |
| `hook` | — (daemon auto-index) | ⤳ | the auto-indexer replaces the post-commit hook |
| `health` / `dead-code` / `decision` / `costs` / `pages` / `pipeline` / `coverage` / `security` | — (dashboard) | ⤳ | these are dashboard views + REST/MCP endpoints, not CLI commands |
| `generate` / `embed` / `export` | — | ⏳ | generation/export moving to dashboard-triggered actions |
| `workspace` (group) | — | ⏳ | superseded by the daemon + shared-DB multi-repo model |

Legend addition: ⤳ = capability kept, but relocated to the dashboard or daemon
rather than the CLI.

---

## MCP tools

The Go port ships **32 tools** — the Python task-oriented synthesis endpoints
(`get_answer`/`get_why`/`get_context`), the graph-traversal tools, *and* the
analytics that back the dashboard. HTTP routes and MCP tools share one read
layer (`internal/insights`), so the surfaces stay in lockstep.

| Python tool | Go equivalent | Status |
|---|---|---|
| `get_overview` | `vor_status` | ✅ |
| `get_symbol` | `vor_symbol` | ✅ |
| `get_callers_callees` | `vor_callers` + `vor_dependents` | ✅ (split in two) |
| `get_dead_code` | `vor_dead_code` | ✅ |
| `get_health` | `vor_health` + `vor_health_findings` | ✅ |
| `search_codebase` | `vor_search` | ✅ substring + `semantic=true` embedding ranking |
| `get_community` | `vor_get_community` | ✅ |
| `get_dependency_path` | `vor_get_dependency_path` | ✅ |
| `get_execution_flows` | `vor_get_execution_flows` | ✅ |
| `get_architecture_diagram` | `vor_get_architecture_diagram` | ✅ structured + optional mermaid |
| `get_context` | `vor_get_context` | ✅ triage card per file/symbol |
| `get_answer` | `vor_get_answer` | ✅ FTS retrieval + grounded synthesis + citations |
| `get_why` | `vor_get_why` | ✅ decision archaeology + synthesised rationale |
| `get_risk` | `vor_risk` | 🟡 blast radius + co-change + ownership + governing decisions; no PR-mode `directive` block yet |
| — | `vor_attention` ➕ | ➕ prioritized "what to look at" digest |
| — | `vor_git_insights` ➕ | ➕ bus-factor distribution, churn, top contributors |
| — | `vor_commit_categories` ➕ | ➕ feature/fix/refactor split |
| — | `vor_modules` / `vor_packages` ➕ | ➕ per-module + package structure |
| — | `vor_dependency_matrix` / `vor_entry_points` / `vor_languages` ➕ | ➕ structure/stack analytics |
| — | `vor_hotspots` / `vor_decisions` / `vor_externals` ➕ | ➕ direct read endpoints |
| — | `vor_pages` / `vor_page` ➕ | ➕ generated docs |
| — | `vor_pipeline_log` ➕ | ➕ observability |
| — | `vor_security` / `vor_security_scan` ➕ | ➕ pattern-based security findings + scan |

Semantic `vor_search` is backed by a vector index (wiki pages embedded into the
`embeddings` table; ranks by cosine similarity, falling back to substring when
no embeddings exist).

**Remaining MCP gap:** the richer `get_risk` PR-mode `directive` block.
Everything else has parity or exceeds it.

**Driving usage.** The generated `CLAUDE.md` opens with proactive tool
directives (call `vor_risk` before editing, etc.), and a Claude Code plugin
(`plugins/claude-code/`) registers the server with auto-activating skills.

---

## Code-health biomarkers

Go has 11 of Python's ~17. The mapping isn't 1:1 — some Go names
consolidate what Python splits.

| Go biomarker | Python equivalent | Status |
|---|---|---|
| `high_complexity` | `complex_method` | ✅ |
| `long_function` | `large_method` | ✅ |
| `deep_nesting` | `nested_complexity` | ✅ |
| `god_class` | (≈ large-class heuristic) | 🟡 |
| `untested_hotspot` | `untested_hotspot` | ✅ (real coverage via LCOV/Cobertura ingest) |
| `brain_method` | `brain_method` | ✅ |
| `hidden_coupling` | `hidden_coupling` | ✅ |
| `duplication` | `dry_violation` | ✅ (Rabin-Karp) |
| `long_parameter_list` | `primitive_obsession` | ✅ (signature proxy) |
| `feature_envy` | `feature_envy` | ✅ |
| `shotgun_surgery` | (co-change breadth) | ✅ |
| — | `bumpy_road` | ⏳ |
| — | `complex_conditional` | ⏳ |
| — | `code_age_volatility` | ⏳ |
| — | `developer_congestion` | ⏳ |
| — | `function_hotspot` | ⏳ |
| — | `knowledge_loss` | ⏳ |
| — | `contradictory_decision` / `stale_governance` / `ungoverned_hotspot` | ⏳ (governance biomarkers) |

Community detection uses **Louvain** modularity (gonum), not a
connected-component placeholder. A pattern-based **security scanner**
(`vor security`, `vor_security` MCP tool) populates
`security_findings`.

Findings can be suppressed per-file and per-biomarker via the `health_rules`
setting (gitignore-glob `pattern` or `path` prefix + `overrides: {biomarker:
disabled}`, or `"*"` for all), now stored in the DB `settings` table and edited
on the dashboard's per-repo Settings page (global + per-repo scopes).
Exclusions are health-only — matched files stay in the graph/search/dead-code.

---

## Languages (tree-sitter parsers)

| Language | Status |
|---|---|
| Go, Python, TypeScript, JavaScript, Rust, Java, C, C++, C# | ✅ |
| Ruby, PHP, Swift, Kotlin, Scala, Lua/Luau | ✅ |

Full-tier languages (Go, Python, …) get heritage + binding analysis;
the Phase 13 additions are Partial/Traversal tier (symbols + imports +
calls). Luau is parsed with the Lua grammar (it is a Lua superset).

---

## LLM providers

| Provider | Status |
|---|---|
| Mock (deterministic) | ✅ |
| Anthropic (Messages API, streaming, prompt cache) | ✅ |
| OpenAI (chat completions + streaming + embeddings) | ✅ |
| Gemini (generateContent + streaming + embeddings) | ✅ |
| Ollama (local /api/chat + /api/embed, NDJSON stream) | ✅ |
| LiteLLM (OpenAI-compatible proxy passthrough) | ✅ |

Every provider speaks its vendor API directly over net/http (no SDK
dependency). Embedders ship for OpenAI, Gemini, Ollama, and Mock. Cost
catalog, rate limiting, retry, and the composing middleware are all done
and provider-agnostic.

---

## Subsystems with full parity

- Persistence: consolidated SQLite + Postgres schema (one `0001_init.sql`
  reflecting Python's end state) plus Go-port migrations for embeddings,
  parse-cache, tracked repos, the `settings` table, and `commit_categories`.
- Traverser: gitignore semantics, binary + generated-file detection.
- Graph: PageRank, Louvain community ids, multi-edge persistence.
- Git intelligence: hotspots, ownership, co-change, bus factor.
- Dead-code detection.
- Decision intelligence: inline markers, ADR, CHANGELOG, commit
  archaeology (4 sources).
- External + API-contract extraction: npm/pypi/cargo/go.mod/nuget deps
  plus OpenAPI/gRPC/GraphQL contracts.
- Pipeline orchestration: phase tracking, run_id grouping, resume,
  incremental parse cache (re-parse only changed files on `update`).

---

## Notable intentional divergences

1. **In-repo web dashboard.** Unlike the original plan (a separate Next.js
   dashboard repo), the Go port ships a React dashboard **compiled into the
   binary** (`go:embed`) and served by the daemon at `/`. Browsing that the
   Python CLI did (health, hotspots, decisions, …) lives here, not in the CLI.

2. **Lean CLI + daemon auto-index.** The CLI is reduced to bootstrap/ops +
   repo lifecycle + a couple of scriptable reads. `update`/`watch`/`hook` are
   gone — the daemon's auto-indexer keeps tracked repos fresh on file change.

3. **Database-backed configuration.** Config moved out of YAML files into a
   `settings` table (global + per-repo scopes, edited on the dashboard). Only
   the DB URL and provider API keys come from the environment (bootstrap).

4. **One global database.** A single DB at `~/.config/vor/vor.db` (or
   `VOR_DB_URL`) holds every repo, keyed by `repository_id` — there is no
   per-repo `.vor/wiki.db`. The daemon serves all of them.

5. **Shared read layer.** HTTP routes and MCP tools both call
   `internal/insights`, so the dashboard and the coding agent answer from
   identical logic.

6. **MCP naming.** Go uses `vor_<entity>` tool names; Python uses `get_<task>`.

7. **Schema consolidation.** One `0001_init.sql` reflecting Python's end state,
   then Go-side migrations — no replay of Python's 24 incremental migrations.
