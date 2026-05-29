# Architecture

Vor is a single Go binary that plays three roles, all over **one shared
database**:

- a **CLI** for indexing and a few scriptable read shortcuts,
- a **daemon** (`vor serve`) that exposes the indexed data over HTTP + MCP,
- an embedded **web dashboard** that the daemon serves.

```
                ┌──────────────────────────┐
   vor (CLI) ───┤  global DB (sqlite/pg)   ├─── vor serve ── :7337
   index/       │  ~/.config/vor/vor.db    │   (one daemon) ├── /            dashboard (embedded SPA)
   register     │  N repos, keyed by       │                ├── /api/...     REST (dashboard, scripts)
                │  repository_id           │                └── /mcp         MCP (AI agents)
                └──────────────────────────┘
```

## One database, many repos

Every table is keyed by `repository_id`, so a single database holds N repos.
The database is resolved at startup (see [configuration.md](./configuration.md)):

- `VOR_DB_URL` if set (e.g. a Postgres URL on a central host), otherwise
- the global SQLite file at `~/.config/vor/vor.db`.

There is no per-repo database — the daemon and every CLI command open the same
global DB.

## The daemon serves everything on one port

`vor serve` builds one [chi](https://github.com/go-chi/chi) router and binds it
to a single address (default `127.0.0.1:7337`). Three surfaces share it:

| Path        | Surface                         | Consumers                       |
| ----------- | ------------------------------- | ------------------------------- |
| `/`, `/*`   | the web dashboard (embedded SPA) | humans, in a browser            |
| `/api/...`  | REST                            | the dashboard, scripts          |
| `/mcp`      | MCP over Streamable HTTP        | AI agents (Claude Code, Cursor) |

Route precedence: `/api` and `/mcp` are matched first; the dashboard is the
lowest-priority `/*` catch-all, which also lets client-side routes (deep links)
fall back to `index.html`.

`vor daemon start` launches `vor serve` in the background and records it in
`~/.local/state/vor/daemon.json`; `vor daemon status` / `stop` / `logs` manage
it. See [dashboard.md](./dashboard.md) for exactly how the UI is built into the
binary and served.

## What the CLI does vs. the daemon

- **CLI** does the cheap, deterministic indexing (`init`, `reindex`) and repo
  lifecycle (`register`, `unregister`, `delete`), plus scriptable reads
  (`status`, `search`) and `claude-md` generation. It opens the global DB
  directly.
- **Daemon** owns the long-running surfaces (HTTP/MCP/dashboard) and keeps the
  index fresh: its **auto-indexer** watches each tracked repo's source tree and
  re-runs an incremental pipeline on change. `vor register .` tells a running
  daemon (over HTTP) to start tracking + watching a repo.

See [cli.md](./cli.md) for the command reference and [mcp.md](./mcp.md) for the
tool surface.

## Migrations

Schema is managed by [goose](https://github.com/pressly/goose); migration SQL
lives under `internal/persistence/migrations/{sqlite,postgres}/` and is embedded
in the binary. Migrations run automatically on **daemon startup** and from the
setup commands that can run before any daemon (`init`, `register`, `db
migrate`). The daemon logs `database ready` with the resolved URL and schema
version on startup.
