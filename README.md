# repowise-go

Go port of [repowise](https://github.com/repowise-dev/repowise) — the codebase intelligence layer for your AI coding agent. Indexes a codebase into a typed dependency graph, mines git history for hotspots and ownership, runs deterministic code-health biomarkers, extracts architectural decisions, generates wiki documentation, and exposes everything over HTTP and MCP.

> Work in progress. See [PORTING_PLAN.md](./PORTING_PLAN.md) for the phased roadmap, library choices, and risk register.

## Two components

repowise-go is built as two cooperating components that share one database:

### 1. The CLI — `repowise`

A terminal tool for **indexing and inspecting** repositories. You run it interactively or from scripts and git hooks. It does the cheap, deterministic work (parse, graph, git, health, decisions) with zero LLM calls, plus the LLM-billed documentation generation on demand.

```bash
repowise init <path>              # full index: traverse → parse → git → graph → deadcode → health → externals → decisions → persist
repowise update <path>            # re-index (incremental intent); auto-regenerates CLAUDE.md
repowise generate --provider anthropic   # LLM wiki pages (file / directory / symbol overviews)
repowise embed <path>             # index wiki pages into the vector store for semantic search
repowise status | health | hotspots | dead-code | decisions | externals | costs | pages   # read views
repowise search <query>           # substring search; add --semantic to rank pages by embedding
repowise watch <path>             # debounced auto-update on file change
repowise hook install             # post-commit auto-sync
repowise workspace add / register # multi-repo registries
```

Every read command accepts `--repo <path>` **or** `--repo-id <id>` so it can address any repo in a shared database without `cd`-ing into it.

### 2. The server — `repowise serve`

A **long-running daemon** that exposes the indexed data over the network. Editor clients and dashboards attach to it instead of each spawning their own process. Two surfaces live on one port:

| Surface | Path | For |
|---|---|---|
| REST API | `/api/repos/{id}/*`, `/api/workspace*` | dashboards, scripts, automation |
| MCP (Streamable HTTP) | `/mcp` | AI agents — Claude Code, Cursor, … |

```bash
repowise serve                       # single repo
repowise serve --workspace           # every repo in one workspace
repowise serve --auto                # every workspace in the user-global registry
```

MCP is also available over stdio (`repowise mcp`) for editors that spawn a child process per repo. The HTTP transport is what makes the **daemon** model work — one process, many clients, optionally many repos.

### How they share state

Both components read/write the same database, selected by `REPOWISE_DB_URL` (or `~/.config/repowise/config.yaml`, or per-repo `.repowise/config.yaml`). Point every `init`/`update`/`serve` at one SQLite file — or a Postgres URL on a central host — and a single daemon serves every indexed repo. Each table is keyed by `repository_id`, so one database cleanly holds N repos.

```
                ┌─────────────────────────┐
   repowise ────┤  shared DB (sqlite/pg)  ├──── repowise serve ──── /api  (dashboards)
   (CLI:        │  N repos, 1 row each in │     (daemon)        └──── /mcp  (AI agents)
    index/      │  repositories + per-    │
    inspect)    │  repo analysis tables)  │
                └─────────────────────────┘
```

### Deployment shapes

```bash
# Single workstation — one daemon for every local repo
export REPOWISE_DB_URL=sqlite:$HOME/.local/state/repowise/all.db
repowise workspace add ~/projects/api  && repowise workspace add ~/projects/web
repowise workspace register .          # remember this workspace root
repowise workspace update              # index all members
repowise serve --auto                  # one daemon, all repos, on :7337

# Central host — shared Postgres, many developers attach
export REPOWISE_DB_URL=postgres://repowise@db.internal/repowise
repowise serve --auto --addr 0.0.0.0:7337
```

## User-global state

The CLI tracks box-level state under XDG directories:

| File | Purpose |
|---|---|
| `~/.config/repowise/config.yaml` | user-global defaults (provider, model, db_url) — slots into the merge chain `defaults → user → repo → env` |
| `~/.local/state/repowise/daemon.json` | the running `serve` instance (pid, addr); `repowise status` reports it |
| `~/.local/state/repowise/workspaces.yaml` | registered workspace roots, consumed by `serve --auto` |
| `~/.local/state/repowise/watched.json` | per-repo watch + update history, shown by `repowise watched list` |

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
| Graph build + PageRank + SCC + multi-edge persistence | ✅ |
| Git intelligence — hotspots, ownership, co-change, bus factor | ✅ |
| Dead code detection | ✅ |
| Code health biomarkers (8: complexity, long_function, deep_nesting, god_class, untested_hotspot, brain_method, hidden_coupling, duplication) | ✅ |
| LLM providers — Mock + **Anthropic / OpenAI / Gemini / Ollama / LiteLLM** + cost/ratelimit/retry middleware | ✅ |
| Embedders — Mock + OpenAI / Gemini / Ollama (real semantic search) | ✅ |
| Documentation generation — file / directory / symbol pages | ✅ |
| Documentation generation — architecture page | ⏳ |
| Decision intelligence — inline markers, ADR, CHANGELOG, commit archaeology | ✅ |
| HTTP API (REST, `/api/repos/{id}/*` + `/api/workspace*`) | ✅ |
| MCP server — stdio **and** Streamable HTTP, 22 tools (LLM-synthesis get_answer/get_why/get_context + graph-traversal community/dependency-path/flows/architecture), per-call repo routing | ✅ |
| Workspace / multi-repo — bulk ops, cross-repo co-change, `serve --auto` | ✅ |
| Pipeline orchestrator — phase tracking, run_id grouping, resume | ✅ |
| Auto CLAUDE.md regeneration on init/update | ✅ |
| Parity testing & polish vs Python (Phase 11) | ⏳ |

## Quick start

```bash
make build
export REPOWISE_DB_URL=sqlite:$PWD/.repowise/wiki.db
./bin/repowise db migrate
./bin/repowise init .                       # index this repo
./bin/repowise status                       # one-screen summary (+ daemon state if running)
./bin/repowise health --refactoring-targets # ranked by impact / effort
./bin/repowise generate --provider mock     # wiki pages (use --provider anthropic with a key)
./bin/repowise embed .                       # index pages for semantic search
./bin/repowise search --semantic "auth flow" # rank pages by embedding similarity
./bin/repowise serve --addr :7337           # HTTP API + MCP daemon
```

Requires Go 1.24+. cgo is needed for the tree-sitter parsers; the persistence, git, provider, server, and workspace packages are pure Go.

## Layout

```
cmd/
  repowise/              # main CLI binary
  repowise-augment/      # Claude Code Grep/Glob enrichment hook
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

## License

AGPL-3.0-only, matching upstream.
