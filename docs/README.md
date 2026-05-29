# Vor documentation

## Guides

- [Architecture](./architecture.md) — the binary's three roles (CLI, daemon,
  dashboard), the one shared database, ports/surfaces, and migrations.
- [The web dashboard](./dashboard.md) — how the UI is built into the binary and
  served by the daemon (no separate server), what it shows, and the dev loop.
- [Configuration](./configuration.md) — DB-backed settings, the bootstrap
  (database URL + provider keys), resolution order, and code-health exclusions.
- [CLI reference](./cli.md) — the lean command set.
- [MCP tools](./mcp.md) — the tool surface, transports, and how agents are
  driven to use them.

## Project docs

- [PARITY.md](./PARITY.md) — feature-by-feature comparison vs. the Python
  original, and the notable intentional divergences.
- [ROADMAP.md](./ROADMAP.md) — what's shipped post-parity and what's next.
- [PORTING_PLAN.md](./PORTING_PLAN.md) — the historical Python→Go port plan.
- [TESTING.md](./TESTING.md) — hands-on review/verification checklist.
- [CHANGELOG.md](./CHANGELOG.md) — release notes.
- [adr/](./adr/) — architecture decision records.

> The repo root keeps only `README.md`, `LICENSE`, `NOTICE`, and `CLAUDE.md`.
> `CLAUDE.md` stays at the root on purpose — it's the agent-context file Vor
> generates into every indexed repo (and that coding agents read), not project
> documentation.
