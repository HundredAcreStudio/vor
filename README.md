# repowise-go

Go port of [repowise](https://github.com/repowise-dev/repowise) — the codebase intelligence layer for your AI coding agent. Five intelligence layers, seventeen MCP tools, multi-repo workspaces, auto-sync hooks.

> Work in progress. See [PORTING_PLAN.md](./PORTING_PLAN.md) for the phased roadmap, library choices, and risk register.

## Status

Phase 0 — foundation scaffolding. Not yet usable.

## Building

```bash
make build
./bin/repowise version
```

Requires Go 1.24+. Some downstream phases require cgo for tree-sitter; pure-Go phases (foundation, persistence, git intelligence, providers, server) build without cgo.

## Layout

See [PORTING_PLAN.md §2](./PORTING_PLAN.md#2-module-layout) for the module layout.

## License

AGPL-3.0-only, matching upstream.
