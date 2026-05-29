# vor

**vor** is a Go port of [**repowise**](https://github.com/repowise-dev/repowise) — the codebase intelligence layer for your AI coding agent. Indexes a codebase into a typed dependency graph, mines git history for hotspots and ownership, runs deterministic code-health biomarkers, extracts architectural decisions, generates wiki documentation, and exposes everything over HTTP and MCP.

> A derivative work of repowise (Python), re-implemented in Go. Not affiliated with or endorsed by the upstream project. Licensed under AGPL-3.0-or-later, the same license as repowise — see [Attribution & license](#attribution--license).

> Work in progress. See [PORTING_PLAN.md](./PORTING_PLAN.md) for the phased roadmap, library choices, and risk register.

## Three roles, one binary

The `vor` binary plays three roles over **one shared database**. Full detail in
[docs/architecture.md](./docs/architecture.md).

```
                ┌──────────────────────────┐
   vor (CLI) ───┤  global DB (sqlite/pg)   ├─── vor serve ── :7337
   index/       │  ~/.config/vor/vor.db    │   (one daemon) ├── /            dashboard (embedded)
   register     │  N repos, keyed by       │                ├── /api/...     REST
                │  repository_id           │                └── /mcp         MCP (AI agents)
                └──────────────────────────┘
```

### 1. The CLI — `vor`

A lean terminal tool for indexing and repo lifecycle, plus a couple of
scriptable reads. The browsing surface lives in the dashboard, and the daemon
keeps the index fresh, so there's no `update`/`watch`/`hook`. Full list in
[docs/cli.md](./docs/cli.md).

```bash
vor register .     # index a repo and have the daemon watch it
vor init .         # index a repo once (standalone, no daemon); regenerates CLAUDE.md
vor reindex .      # wipe + re-run the full pipeline
vor status         # one-screen summary
vor search <q>     # symbol search (--semantic ranks wiki pages by embedding)
vor claude-md      # write the codebase-intelligence section into CLAUDE.md
vor doctor         # config / DB / parsers / provider-key diagnostics
```

Reads/mutations accept `--repo <path>` or `--repo-id <id>` to address any repo
in the shared database without `cd`-ing into it.

### 2. The daemon — `vor serve`

A long-running process that serves three surfaces on one port. Clients attach
to it instead of spawning their own.

| Path        | Surface                  | For                          |
| ----------- | ------------------------ | ---------------------------- |
| `/`         | web dashboard (embedded) | humans, in a browser         |
| `/api/...`  | REST                     | the dashboard, scripts       |
| `/mcp`      | MCP (Streamable HTTP)    | AI agents (Claude Code, …)   |

```bash
vor daemon start      # launch `vor serve` in the background → http://127.0.0.1:7337/
vor daemon status     # pid, addr, db url
vor daemon logs -f    # follow the daemon log
```

MCP is also available over stdio (`vor mcp`). The daemon's auto-indexer watches
each tracked repo and re-runs an incremental pipeline on change.

### 3. The dashboard

A React SPA **compiled into the binary** and served by the daemon at `/` — no
separate server, no Node at runtime. Repo overview (health, attention, git
insights, dependency graph, …), wiki, search, and per-repo settings. How it's
built and served: [docs/dashboard.md](./docs/dashboard.md).

## Configuration

Configuration lives in the **database** (a `settings` table), not in YAML
files. The database URL and provider API keys come from the environment /
defaults (bootstrap); everything else resolves
`defaults → global → per-repo → VOR_* env`. The dashboard's per-repo
**Settings** page edits it (including code-health exclusions). Details and the
full settings key list: [docs/configuration.md](./docs/configuration.md).

- **Database:** `VOR_DB_URL` (e.g. a Postgres URL), else the global SQLite file
  `~/.config/vor/vor.db`. One DB holds every repo.
- **API keys:** `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `GEMINI_API_KEY` /
  `OPENROUTER_API_KEY` — env only, never persisted.

## Documentation

| Doc | What's in it |
|---|---|
| [docs/architecture.md](./docs/architecture.md) | CLI / daemon / dashboard, the shared DB, ports, migrations |
| [docs/dashboard.md](./docs/dashboard.md) | how the UI is embedded + served by the daemon; the dev loop |
| [docs/configuration.md](./docs/configuration.md) | DB-backed settings, bootstrap, health exclusions |
| [docs/cli.md](./docs/cli.md) | the lean command set |
| [docs/mcp.md](./docs/mcp.md) | MCP tools, transports, driving agent usage |
| [PARITY.md](./PARITY.md) | feature-by-feature comparison vs. the Python original |

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
| Coverage-aware health — `untested_hotspot` biomarker from imported LCOV / Cobertura data | ✅ |
| Security scan — secrets / weak crypto / injection sinks (`vor security`) | ✅ |
| Config health exclusions — per-file / per-check via `health_rules` (gitignore globs) | ✅ |
| LLM providers — Mock + **Anthropic / OpenAI / Gemini / Ollama / LiteLLM** + cost/ratelimit/retry middleware | ✅ |
| Embedders — Mock + OpenAI / Gemini / Ollama (real semantic search) | ✅ |
| Documentation generation — file / directory / symbol pages | ✅ |
| Documentation generation — architecture page | ⏳ |
| Decision intelligence — inline markers, ADR, CHANGELOG, commit archaeology | ✅ |
| HTTP API (REST, `/api/repos/{id}/*`) | ✅ |
| Web dashboard — React SPA embedded via `go:embed`, served by the daemon at `/` | ✅ |
| Database-backed configuration (`settings` table; global + per-repo) | ✅ |
| MCP server — stdio **and** Streamable HTTP, 32 tools (incl. `vor_risk`, `vor_attention`, git/commit/module insights, LLM synthesis, graph traversal, security), per-call repo routing | ✅ |
| Claude Code plugin — registers the MCP server + skills that drive tool usage | ✅ |
| Pipeline orchestrator — phase tracking, run_id grouping, resume | ✅ |
| `serve` prints MCP client-install instructions (Claude Code / mcp.json) at startup | ✅ |
| Auto CLAUDE.md regeneration on init/update | ✅ |
| Parity testing & polish vs Python (Phase 11) | ⏳ |

## Quick start

```bash
make all                 # build the UI (ui/dist) + the binaries
make install             # install `vor` to $GOBIN

vor daemon start         # start the daemon (migrates the global DB on startup)
vor register .           # index this repo and have the daemon watch it
open http://127.0.0.1:7337/   # the dashboard

vor status               # one-screen summary
vor search "auth"        # symbol search
```

`make build` alone is enough if you don't need to rebuild the UI — it embeds the
committed `ui/dist`. Requires Go 1.25+ and (for `make ui`) Node. cgo is needed
for the tree-sitter parsers; the persistence, git, provider, and server packages
are pure Go.

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
    health/              # code-health biomarkers
  insights/              # shared read layer (attention, risk, git/module insights) — HTTP + MCP
  generation/            # wiki page generation (context → templates → pages → wikistore)
  providers/             # LLM interface + Mock/Anthropic/OpenAI/Gemini/Ollama/LiteLLM + middleware
  persistence/           # SQLite/Postgres schema, repository CRUD, per-domain stores, migrations
  pipeline/              # phase orchestrator (run_id grouping + resume)
  server/
    http/                # chi REST API; mounts MCP at /mcp and the dashboard at /
    mcp/                 # mark3labs/mcp-go server (stdio + Streamable HTTP)
    autoindex/           # daemon watcher: re-index tracked repos on change
    registry/            # live register/unregister of tracked repos
  userconfig/            # ~/.config + ~/.local/state (global DB path, daemon record)
  cli/commands/          # cobra subcommands
  config/                # bootstrap + DB-backed settings resolution
ui/                      # React + Vite dashboard; ui/dist is embedded via go:embed
plugins/claude-code/     # Claude Code plugin: .mcp.json + tool-usage skills
testdata/                # fixtures for ingest demos + integration tests
```

See [docs/architecture.md](./docs/architecture.md) for how these fit together.

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
under the **same license: AGPL-3.0-or-later**. See [`LICENSE`](./LICENSE) for
the full text and [`NOTICE`](./NOTICE) for the attribution and
copyright notices.

If you use repowise's ideas here in a networked service, the AGPL's
network-use clause (§13) applies: you must offer users the corresponding
source.
