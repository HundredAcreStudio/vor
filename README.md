# repowise-go

Go port of [repowise](https://github.com/repowise-dev/repowise) — the codebase intelligence layer for your AI coding agent. Indexes a codebase into a typed dependency graph, mines git history for hotspots and ownership, runs deterministic code-health biomarkers, and exposes everything via HTTP and MCP.

> Work in progress. See [PORTING_PLAN.md](./PORTING_PLAN.md) for the phased roadmap, library choices, and risk register.

## Status

End-to-end pipeline working for six languages (Go, Python, TypeScript, JavaScript, Rust, Java). Five of the fifteen Python health biomarkers, two of the seventeen MCP tools' base read paths, and the HTTP API surface are live. LLM-powered generation (Phase 5) and the Anthropic / OpenAI provider implementations (Phase 4 Pass B) are still to land.

| Subsystem | Status |
|---|---|
| Persistence (28 tables, SQLite + Postgres) | ✅ |
| Traverser (gitignore, binary detection, generated-file detection) | ✅ |
| Parsers — Go / Python / TypeScript / JavaScript / Rust / Java | ✅ |
| Parsers — C / C++ / C# / Ruby / PHP / Swift / Kotlin / Scala / Luau | ⏳ |
| External-system extractors — npm / pypi / cargo / go.mod / nuget | ✅ |
| Graph build + PageRank + SCC + multi-edge persistence | ✅ |
| Git intelligence — hotspots, ownership, co-change, bus factor | ✅ |
| Dead code detection | ✅ |
| Code health biomarkers (5 of 15: complexity, long_function, deep_nesting, god_class, untested_hotspot) | 🟡 |
| LLM provider abstraction + Mock + rate-limit + retry + cost catalog | ✅ |
| Anthropic / OpenAI / Gemini provider implementations | ⏳ |
| HTTP API (8 route packages, /api/repos/{id}/...) | ✅ |
| MCP server stdio transport, 10 tools | ✅ |
| Documentation generation (Phase 5) | ⏳ |
| Decision intelligence (Phase 6 Pass D) | ⏳ |
| Workspace / multi-repo (Phase 10) | ⏳ |

## Quick start

```bash
make build
./bin/repowise ingest <path> --persist     # walk + parse + graph + git + health + externals → SQLite
./bin/repowise status --repo <path>        # one-screen summary
./bin/repowise health --repo <path>        # code health report
./bin/repowise dead-code --repo <path>     # unreachable files / symbols
./bin/repowise hotspots --repo <path>      # high-churn files
./bin/repowise search QUERY --repo <path>  # find symbols by name
./bin/repowise externals --repo <path>     # declared third-party deps
./bin/repowise doctor --repo <path>        # diagnostic checks
./bin/repowise serve --repo <path>         # HTTP API on :7337
./bin/repowise mcp --repo <path>           # MCP server over stdio (for Claude Code, Cursor)
```

Requires Go 1.24+. cgo is needed for the tree-sitter parsers; the persistence, git, provider, and server packages are pure Go.

## Layout

```
cmd/
  repowise/              # main CLI binary
  repowise-augment/      # Claude Code Grep/Glob enrichment hook (stub)
internal/
  ingestion/             # traverser + parsers + graph + git + external systems
  analysis/              # dead code + code health biomarkers
  providers/             # LLM provider interface + Mock + rate limit + retry + cost
  persistence/           # SQLite/Postgres schema, repository CRUD, stores per domain
  server/
    http/                # chi-based REST API
    mcp/                 # mark3labs/mcp-go server over stdio
  cli/commands/          # cobra subcommands
  config/                # .repowise/config.yaml + env overlay
  logging/               # slog setup
testdata/                # sample repo for ingest demos + tests
```

See [PORTING_PLAN.md §2](./PORTING_PLAN.md#2-module-layout) for the full layout and library choices.

## Testing

```bash
go test ./...
```

Every package has tests. Persistence tests open a real SQLite DB and run the actual migrations. Parser tests run tree-sitter against hand-written source. HTTP tests use `httptest.NewServer`; MCP tests use `HandleMessage` directly. Git tests build real git repos in `t.TempDir()` via `go-git`.

## License

AGPL-3.0-only, matching upstream.
