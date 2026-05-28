# vor

**vor** is a Go port of [**repowise**](https://github.com/repowise-dev/repowise) — the codebase intelligence layer for your AI coding agent. Indexes a codebase into a typed dependency graph, mines git history for hotspots and ownership, runs deterministic code-health biomarkers, extracts architectural decisions, generates wiki documentation, and exposes everything over HTTP and MCP.

> A derivative work of repowise (Python), re-implemented in Go. Not affiliated with or endorsed by the upstream project. Licensed under AGPL-3.0-only, the same license as repowise — see [Attribution & license](#attribution--license).

> Work in progress. See [PORTING_PLAN.md](./PORTING_PLAN.md) for the phased roadmap, library choices, and risk register.

## Two components

vor-go is built as two cooperating components that share one database:

### 1. The CLI — `vor`

A terminal tool for **indexing and inspecting** repositories. You run it interactively or from scripts and git hooks. It does the cheap, deterministic work (parse, graph, git, health, decisions) with zero LLM calls, plus the LLM-billed documentation generation on demand.

```bash
vor init <path>              # full index: traverse → parse → git → graph → deadcode → health → externals → decisions → persist
vor update <path>            # re-index (incremental intent); auto-regenerates CLAUDE.md
vor generate --provider anthropic   # LLM wiki pages (file / directory / symbol overviews)
vor embed <path>             # index wiki pages into the vector store for semantic search
vor status | health | hotspots | dead-code | decisions | externals | costs | pages   # read views
vor coverage import <file>   # ingest LCOV/Cobertura → untested_hotspot
vor security scan            # secrets / weak crypto / injection sinks
vor search <query>           # substring search; add --semantic to rank pages by embedding
vor watch <path>             # debounced auto-update on file change
vor hook install             # post-commit auto-sync
vor workspace add / register # multi-repo registries
```

Every read command accepts `--repo <path>` **or** `--repo-id <id>` so it can address any repo in a shared database without `cd`-ing into it.

### 2. The server — `vor serve`

A **long-running daemon** that exposes the indexed data over the network. Editor clients and dashboards attach to it instead of each spawning their own process. Two surfaces live on one port:

| Surface | Path | For |
|---|---|---|
| REST API | `/api/repos/{id}/*`, `/api/workspace*` | dashboards, scripts, automation |
| MCP (Streamable HTTP) | `/mcp` | AI agents — Claude Code, Cursor, … |

```bash
vor serve                       # single repo
vor serve --workspace           # every repo in one workspace
vor serve --auto                # every workspace in the user-global registry
```

MCP is also available over stdio (`vor mcp`) for editors that spawn a child process per repo. The HTTP transport is what makes the **daemon** model work — one process, many clients, optionally many repos.

### How they share state

Both components read/write the same database, selected by `VOR_DB_URL` (or `~/.config/vor/config.yaml`, or per-repo `.vor/config.yaml`). Point every `init`/`update`/`serve` at one SQLite file — or a Postgres URL on a central host — and a single daemon serves every indexed repo. Each table is keyed by `repository_id`, so one database cleanly holds N repos.

```
                ┌─────────────────────────┐
   vor ────┤  shared DB (sqlite/pg)  ├──── vor serve ──── /api  (dashboards)
   (CLI:        │  N repos, 1 row each in │     (daemon)        └──── /mcp  (AI agents)
    index/      │  repositories + per-    │
    inspect)    │  repo analysis tables)  │
                └─────────────────────────┘
```

### Deployment shapes

```bash
# Single workstation — one daemon for every local repo
export VOR_DB_URL=sqlite:$HOME/.local/state/vor/all.db
vor workspace add ~/projects/api  && vor workspace add ~/projects/web
vor workspace register .          # remember this workspace root
vor workspace update              # index all members
vor serve --auto                  # one daemon, all repos, on :7337

# Central host — shared Postgres, many developers attach
export VOR_DB_URL=postgres://vor@db.internal/vor
vor serve --auto --addr 0.0.0.0:7337
```

## User-global state

The CLI tracks box-level state under XDG directories:

| File | Purpose |
|---|---|
| `~/.config/vor/config.yaml` | user-global defaults (provider, model, health_rules) — slots into the merge chain `defaults → user → repo → env` |
| `~/.local/state/vor/daemon.json` | the running `serve` instance (pid, addr); `vor status` reports it |
| `~/.local/state/vor/workspaces.yaml` | registered workspace roots, consumed by `serve --auto` |
| `~/.local/state/vor/watched.json` | per-repo watch + update history, shown by `vor watched list` |

## Configuration

Settings load through a merge chain — each layer overrides the previous:

```
defaults → ~/.config/vor/config.yaml → <repo>/.vor/config.yaml → VOR_* env
```

`vor init` writes a commented `<repo>/.vor/config.yaml` on first run (and never clobbers an existing one). Provider API keys are read from the environment only, never the file, so they can't be committed by accident.

### Code-health exclusions (`health_rules`)

Suppress biomarker findings for files matching a glob (`pattern`, gitignore syntax) or a path prefix (`path`). `overrides` maps a biomarker name to an action — `disabled` (also `off` / `skip` / `ignore`) turns that check off, and the `"*"` key applies to every biomarker. Exclusions are **health-only**: matched files still appear in the dependency graph, search, and dead-code analysis — they just stop generating health findings (and stop dragging the file's score). Rules from the user-global and repo-local files are **additive**, so a global "ignore tests" rule and a repo-specific rule both apply.

```yaml
# <repo>/.vor/config.yaml
health_rules:
  - pattern: "**/*_test.go"        # table-driven tests aren't real complexity debt
    overrides:
      high_complexity: disabled
      long_function: disabled
      brain_method: disabled
  - path: "internal/generated/"    # suppress every biomarker under a prefix
    overrides:
      "*": disabled
```

## Status

For a feature-by-feature comparison against the Python implementation
(CLI commands, MCP tools, biomarkers, languages, providers), see
[PARITY.md](./PARITY.md).

| Subsystem | Status |
|---|---|
| Persistence (28 tables, SQLite + Postgres) | ✅ |
| Traverser (gitignore, binary + generated-file detection) | ✅ |
| Parsers — Go / Python / TypeScript / JavaScript / Rust / Java / C / C++ / C# | ✅ |
| Parsers — Ruby / PHP / Swift / Kotlin / Scala / Lua-Luau | ✅ |
| External extractors — npm / pypi / cargo / go.mod / nuget | ✅ |
| API-contract extraction — OpenAPI/Swagger endpoints, gRPC services, GraphQL types | ✅ |
| Incremental indexing — content-hash parse cache (`update` re-parses only changed files) | ✅ |
| Graph build + PageRank + SCC + multi-edge persistence | ✅ |
| Git intelligence — hotspots, ownership, co-change, bus factor | ✅ |
| Dead code detection | ✅ |
| Code health biomarkers (11: complexity, long_function, deep_nesting, god_class, untested_hotspot, brain_method, hidden_coupling, duplication, long_parameter_list, feature_envy, shotgun_surgery) | ✅ |
| Community detection — Louvain modularity (gonum) | ✅ |
| Coverage ingest — LCOV / Cobertura → untested_hotspot (`vor coverage import`) | ✅ |
| Security scan — secrets / weak crypto / injection sinks (`vor security`) | ✅ |
| Config health exclusions — per-file / per-check via `health_rules` (gitignore globs) | ✅ |
| LLM providers — Mock + **Anthropic / OpenAI / Gemini / Ollama / LiteLLM** + cost/ratelimit/retry middleware | ✅ |
| Embedders — Mock + OpenAI / Gemini / Ollama (real semantic search) | ✅ |
| Documentation generation — file / directory / symbol pages | ✅ |
| Documentation generation — architecture page | ⏳ |
| Decision intelligence — inline markers, ADR, CHANGELOG, commit archaeology | ✅ |
| HTTP API (REST, `/api/repos/{id}/*` + `/api/workspace*`) | ✅ |
| MCP server — stdio **and** Streamable HTTP, 25 tools (LLM-synthesis get_answer/get_why/get_context + graph-traversal community/dependency-path/flows/architecture + security + async reindex/security_scan mutations), per-call repo routing | ✅ |
| Workspace / multi-repo — bulk ops, cross-repo co-change, `serve --auto` | ✅ |
| Pipeline orchestrator — phase tracking, run_id grouping, resume | ✅ |
| `serve` prints MCP client-install instructions (Claude Code / mcp.json) at startup | ✅ |
| Auto CLAUDE.md regeneration on init/update | ✅ |
| Parity testing & polish vs Python (Phase 11) | ⏳ |

## Quick start

```bash
make build
export VOR_DB_URL=sqlite:$PWD/.vor/wiki.db
./bin/vor db migrate
./bin/vor init .                       # index this repo
./bin/vor status                       # one-screen summary (+ daemon state if running)
./bin/vor health --refactoring-targets # ranked by impact / effort
./bin/vor generate --provider mock     # wiki pages (use --provider anthropic with a key)
./bin/vor embed .                       # index pages for semantic search
./bin/vor search --semantic "auth flow" # rank pages by embedding similarity
./bin/vor serve --addr :7337           # HTTP API + MCP daemon
```

Requires Go 1.24+. cgo is needed for the tree-sitter parsers; the persistence, git, provider, server, and workspace packages are pure Go.

## Layout

```
cmd/
  vor/              # main CLI binary
  vor-augment/      # Claude Code Grep/Glob enrichment hook
internal/
  ingestion/             # traverser + parsers + graph + git + external systems
  analysis/
    deadcode/            # unreachable file/symbol detection
    decisions/           # inline / ADR / changelog / commit extractors
    health/              # 8 code-health biomarkers
  generation/            # wiki page generation (context → templates → pages → wikistore)
  providers/             # LLM interface + Mock/Anthropic/OpenAI/Gemini/Ollama/LiteLLM + cost/ratelimit/retry middleware
  persistence/           # SQLite/Postgres schema, repository CRUD, per-domain stores
  pipeline/              # phase orchestrator (run_id grouping + resume)
  server/
    http/                # chi REST API (mounts MCP at /mcp)
    mcp/                 # mark3labs/mcp-go server (stdio + Streamable HTTP)
  workspace/             # multi-repo registry + cross-repo co-change
  userconfig/            # ~/.config + ~/.local/state (config, daemon, workspaces, watched)
  cli/commands/          # cobra subcommands
  config/                # config merge chain (defaults → user → repo → env)
testdata/                # fixtures for ingest demos + integration tests
```

See [PORTING_PLAN.md §2](./PORTING_PLAN.md#2-module-layout) for the full layout and library choices.

## Testing

```bash
go test ./...
```

Every package has tests. Persistence tests open a real SQLite DB and run the actual migrations. Parser tests run tree-sitter against hand-written source. HTTP tests use `httptest.NewServer`; MCP tests drive both `HandleMessage` (stdio) and the Streamable HTTP transport. Git tests build real git repos in `t.TempDir()` via `go-git`.

## Attribution & license

vor is an independent Go re-implementation (port) of
[**repowise**](https://github.com/repowise-dev/repowise), a Python project
by the repowise authors. It tracks repowise's design and feature set —
the dependency-graph model, code-health biomarkers, decision mining, and
the MCP tool surface all derive from that original work. It is **not**
affiliated with or endorsed by the upstream project.

Because it is a derivative of AGPL-licensed software, vor is released
under the **same license: AGPL-3.0-only**. See [`LICENSE`](./LICENSE) for
the full text and [`NOTICE`](./NOTICE) for the attribution and
copyright notices.

If you use repowise's ideas here in a networked service, the AGPL's
network-use clause (§13) applies: you must offer users the corresponding
source.
