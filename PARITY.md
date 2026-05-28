# Parity with the Python implementation

Tracks how the Go port (`repowise-go`) compares to upstream
[repowise](https://github.com/repowise-dev/repowise) (Python, v0.12.x).
Phase 11 reference — updated as gaps close.

Legend: ✅ full parity · 🟡 partial / behavioural difference · ⏳ not yet · ➕ Go-only addition

---

## CLI commands

The Go port has **every** documented Python command, plus Go-specific
observability commands.

| Python | Go | Status | Notes |
|---|---|---|---|
| `init` | `init` | ✅ | Go also auto-regenerates CLAUDE.md after init |
| `update` | `update` | ✅ | |
| `reindex` | `reindex` | ✅ | Go requires `--yes` (destructive) |
| `delete` | `delete` | ✅ | Go requires `--yes` |
| `serve` | `serve` | ✅ | Go adds `--workspace`, `--auto`, `--mcp`, MCP over HTTP at `/mcp` |
| `mcp` | `mcp` | ✅ | Go adds `--workspace` mode |
| `status` | `status` | ✅ | Go also reports daemon state + watched footer |
| `doctor` | `doctor` | ✅ | |
| `costs` | `costs` | ✅ | |
| `dead-code` | `dead-code` | ✅ | |
| `health` | `health` | 🟡 | `--file`/`--module`/`--refactoring-targets` done; `--trend`/`--coverage` are stubs (need snapshot table + lcov parser) |
| `search` | `search` | 🟡 | LIKE-based; full FTS5 search exists for pages but not symbol search |
| `decision` (group) | `decision` (group) | ✅ | list/show/add/confirm/dismiss/deprecate/health |
| `export` | `export` | 🟡 | markdown/json full; html is `<pre>` fallback (no markdown→HTML renderer) |
| `generate-claude-md` | `claude-md` (alias `generate-claude-md`) | ✅ | byte-identical marker format |
| `hook` (group) | `hook` (group) | ✅ | install/uninstall/status; identical post-commit script |
| `watch` | `watch` | ✅ | Go adds `--workspace` (per-repo watchers) |
| `workspace` (group) | `workspace` (group) | 🟡 | list/add/remove/scan/set-default + Go-only register/registered/status/update/hook/doctor/co-changes; no cross-repo **contracts** yet |
| `augment` | `repowise-augment` binary | ✅ | separate binary |
| — | `generate` ➕ | ➕ | Go splits LLM generation into its own command (cost control) |
| — | `pages` (group) ➕ | ➕ | read side of generation |
| — | `pipeline` (group) ➕ | ➕ | run_id history / status / resume |
| — | `watched` (group) ➕ | ➕ | user-global watch registry |
| — | `db` ➕ | ➕ | migration management |
| — | `ingest` ➕ | ➕ | lower-level walk+parse (pre-`init`) |

---

## MCP tools

The two implementations diverge in **philosophy**. Python's tools are
task-oriented synthesis endpoints (`get_answer`, `get_why`,
`get_context`) that call the LLM to compose an answer. The Go port
ships the entity-oriented read primitives those compose from, but not
yet the LLM-synthesis layer.

| Python tool | Go equivalent | Status |
|---|---|---|
| `get_overview` | `repowise_status` | 🟡 entity vs synthesised narrative |
| `get_symbol` | `repowise_symbol` | ✅ |
| `get_callers_callees` | `repowise_callers` + `repowise_dependents` | ✅ (split in two) |
| `get_dead_code` | `repowise_dead_code` | ✅ |
| `get_health` | `repowise_health` + `repowise_health_findings` | ✅ |
| `search_codebase` | `repowise_search` | ✅ substring (default) + `semantic=true` embedding ranking |
| `get_graph_metrics` | `repowise_status` (partial) | 🟡 |
| `get_community` | `repowise_get_community` | ✅ |
| `get_dependency_path` | `repowise_get_dependency_path` | ✅ |
| `get_execution_flows` | `repowise_get_execution_flows` | ✅ |
| `get_architecture_diagram` | `repowise_get_architecture_diagram` | ✅ structured + optional mermaid |
| `get_context` | `repowise_get_context` | ✅ triage card per file/symbol (pure data) |
| `get_answer` | `repowise_get_answer` | ✅ FTS retrieval + grounded synthesis + citations |
| `get_why` | `repowise_get_why` | ✅ decision archaeology + synthesised rationale |
| `get_risk` | `repowise_hotspots` (partial) | 🟡 churn data without the PR `directive` block |
| — | `repowise_pages` / `repowise_page` ➕ | ➕ generated docs |
| — | `repowise_pipeline_log` ➕ | ➕ observability |
| — | `repowise_workspace_repos` ➕ | ➕ multi-repo discovery |

The LLM-synthesis tools (`get_answer`, `get_context`, `get_why`) and
the graph-traversal tools (`get_community`, `get_dependency_path`,
`get_execution_flows`, `get_architecture_diagram`) are all ported.
Synthesis tools degrade gracefully without a provider; graph tools
are deterministic and need none.

Semantic `search_codebase` is now backed by a vector index: `repowise
embed` embeds wiki pages with the configured embedder (default `mock`;
real embedders register by name) into the `embeddings` table, and
`repowise_search` / `repowise search --semantic` rank pages by cosine
similarity, falling back to the substring path when no embeddings exist.

**Remaining MCP gaps:** the richer `get_risk` PR-mode `directive`
block. Everything else has parity.

---

## Code-health biomarkers

Go has 8 of Python's ~17. The mapping isn't 1:1 — some Go names
consolidate what Python splits.

| Go biomarker | Python equivalent | Status |
|---|---|---|
| `high_complexity` | `complex_method` | ✅ |
| `long_function` | `large_method` | ✅ |
| `deep_nesting` | `nested_complexity` | ✅ |
| `god_class` | (≈ large-class heuristic) | 🟡 |
| `untested_hotspot` | `untested_hotspot` | ✅ |
| `brain_method` | `brain_method` | ✅ |
| `hidden_coupling` | `hidden_coupling` | ✅ |
| `duplication` | `dry_violation` | ✅ (Rabin-Karp) |
| — | `bumpy_road` | ⏳ |
| — | `complex_conditional` | ⏳ |
| — | `code_age_volatility` | ⏳ |
| — | `coverage_gap` | ⏳ (needs lcov ingest) |
| — | `developer_congestion` | ⏳ |
| — | `function_hotspot` | ⏳ |
| — | `knowledge_loss` | ⏳ |
| — | `primitive_obsession` | ⏳ |
| — | `contradictory_decision` / `stale_governance` / `ungoverned_hotspot` | ⏳ (governance biomarkers) |

---

## Languages (tree-sitter parsers)

| Language | Status |
|---|---|
| Go, Python, TypeScript, JavaScript, Rust, Java, C, C++, C# | ✅ |
| Ruby, PHP, Swift, Kotlin, Scala, Luau | ⏳ |

---

## LLM providers

| Provider | Status |
|---|---|
| Mock (deterministic) | ✅ |
| Anthropic (Messages API, streaming, prompt cache) | ✅ |
| OpenAI | ⏳ |
| Gemini | ⏳ |
| Ollama / LiteLLM passthrough | ⏳ |

Cost catalog, rate limiting, retry, and the middleware that composes
them are all done and provider-agnostic.

---

## Subsystems with full parity

- Persistence: 28-table schema (SQLite + Postgres), consolidates
  Python's 24 alembic migrations.
- Traverser: gitignore semantics, binary + generated-file detection.
- Graph: PageRank, SCC/community ids, multi-edge persistence.
- Git intelligence: hotspots, ownership, co-change, bus factor.
- Dead-code detection.
- Decision intelligence: inline markers, ADR, CHANGELOG, commit
  archaeology (4 sources).
- Pipeline orchestration: phase tracking, run_id grouping, resume.

---

## Notable intentional divergences

1. **Generation is a separate command.** Python regenerates docs inside
   `update`; Go splits `generate` out so LLM spend is explicit and
   opt-in. (CLAUDE.md regeneration, which is LLM-free, still runs
   automatically on init/update.)

2. **MCP naming.** Go uses `repowise_<entity>` tool names; Python uses
   `get_<task>`. A future synthesis layer can add the task-oriented
   tools on top of the existing primitives without renaming them.

3. **Daemon-first multi-repo.** Go's `serve --auto` + shared-DB model
   (one process, N repos, `repository_id`-keyed tables) is a Go-port
   addition aimed at central-host deployment. Python's workspace model
   is more per-invocation.

4. **Schema consolidation.** One migration (`0001_init.sql`) instead of
   24 incremental ones — the Go port started from the final Python
   schema rather than replaying history.
