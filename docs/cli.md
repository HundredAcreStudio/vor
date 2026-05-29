# CLI reference

The CLI is intentionally lean: bootstrap/ops, repo lifecycle, and a couple of
scriptable read shortcuts. Browsing (health, hotspots, decisions, dead code,
security, costs, …) lives in the [dashboard](./dashboard.md); the daemon's
auto-indexer keeps the index fresh, so there are no manual `update`/`watch`/
`hook` commands.

Everything runs against the one global database
(see [configuration.md](./configuration.md)).

## Server & daemon

| Command          | What it does                                                       |
| ---------------- | ----------------------------------------------------------------- |
| `vor serve`      | Run the HTTP API + MCP + dashboard in the foreground.             |
| `vor daemon start` | Launch `vor serve` in the background (records `daemon.json`).   |
| `vor daemon stop` / `status` / `restart` / `logs` | Manage the background daemon. `logs -f` follows; `logs -n N` tails. |
| `vor mcp`        | Run the MCP server over stdio (for editors that spawn a child).  |

## Repo lifecycle

| Command            | What it does                                                    |
| ------------------ | -------------------------------------------------------------- |
| `vor register .`   | Track a repo/worktree: index it and have the daemon watch it. `--ephemeral` for disposable worktrees (purged on unregister). |
| `vor unregister .` | Stop tracking a repo (purges it if ephemeral).                 |
| `vor init .`       | Index a repo through the full pipeline once (no watching). Also regenerates the repo's `CLAUDE.md`. |
| `vor reindex .`    | Wipe a repo's persisted state and re-run the full pipeline.    |
| `vor delete .`     | Drop a repo's persisted index (files on disk untouched). `--yes` to skip the confirm. |

> In daemon mode, `vor register` is the usual entry point — it hands off to a
> running daemon over HTTP, which indexes and then watches the repo. `init` is
> the standalone "index once" path when you're not running a daemon.

## Reads & generation

| Command          | What it does                                                       |
| ---------------- | ----------------------------------------------------------------- |
| `vor status`     | One-screen summary of the latest indexed state (+ daemon state).  |
| `vor search <q>` | Substring search over symbols; `--semantic` ranks wiki pages by embedding. |
| `vor claude-md`  | Write/update `<repo>/CLAUDE.md` with the codebase-intelligence section (+ proactive MCP tool directives). |

## Admin

| Command            | What it does                                                    |
| ------------------ | -------------------------------------------------------------- |
| `vor db migrate` / `db status` | Run / inspect database migrations.                 |
| `vor doctor`       | Diagnostics: configuration, DB reachability, parsers, provider keys. |
| `vor completion`   | Generate (and optionally install) shell completion.            |
| `vor version`      | Version information.                                           |

## Addressing a repo

Commands that read or mutate a specific repo accept `--repo <path>` or
`--repo-id <id>`, so you can target any repo in the shared database without
`cd`-ing into it.
