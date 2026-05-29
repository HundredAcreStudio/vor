# Vor documentation

- [Architecture](./architecture.md) — the binary's three roles (CLI, daemon,
  dashboard), the one shared database, ports/surfaces, and migrations.
- [The web dashboard](./dashboard.md) — how the UI is built into the binary and
  served by the daemon (no separate server), what it shows, and the dev loop.
- [Configuration](./configuration.md) — DB-backed settings, the bootstrap
  (database URL + provider keys), resolution order, and code-health exclusions.
- [CLI reference](./cli.md) — the lean command set.
- [MCP tools](./mcp.md) — the tool surface, transports, and how agents are
  driven to use them.

Other top-level docs: [PARITY.md](../PARITY.md), [ROADMAP.md](../ROADMAP.md),
[TESTING.md](../TESTING.md), [CHANGELOG.md](../CHANGELOG.md),
[PORTING_PLAN.md](../PORTING_PLAN.md), and architecture decision records under
[`docs/adr/`](./adr/).
