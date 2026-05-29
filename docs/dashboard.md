# The web dashboard

Vor ships a web dashboard. It is **not a separate service** — it's a React SPA
compiled into the `vor` binary and served by the daemon on the same port as the
API. Start the daemon and the dashboard is live.

```
http://127.0.0.1:7337/
```

## How it runs (production)

1. `make ui` builds the React app (`ui/`) into `ui/dist`.
2. `ui/embed.go` embeds that build into the binary:
   ```go
   //go:embed all:dist
   var distFS embed.FS
   ```
3. The HTTP server mounts `ui.Handler()` as the `/*` catch-all
   (`internal/server/http/server.go`), serving static assets and falling back
   to `index.html` for client-side routes (so deep links / reloads work).
4. `vor serve` runs that HTTP server; `vor daemon start` launches `vor serve` in
   the background.

So there is **one process** (the daemon) listening on **one port**, serving the
dashboard at `/`, the REST API at `/api/...`, and MCP at `/mcp`. No Node
runtime, no separate UI server, nothing extra to launch. Stopping the daemon
stops the dashboard.

> The committed `ui/dist/` is the source of truth the binary embeds, so a plain
> `go build` (no Node) always produces a working dashboard. Rebuild it with
> `make ui` and commit the result whenever the UI changes.

## The UI talks only to the API

The browser never touches the database. The SPA (`ui/src/api.ts`) fetches
exclusively from the `/api/*` endpoints; all database access is server-side Go.
That's also why the same logic is reusable as MCP tools — see
[mcp.md](./mcp.md).

## What it shows

The dashboard has a far-left icon rail and a contextual sidebar:

- **Repositories** — cards per indexed repo (files, symbols, findings, health);
  add a repo by absolute path (→ `POST /api/repos/register`).
- **Repo views** (click a repo): **Overview** (health gauge, KPIs, Attention
  digest, languages donut, hotspots/decisions/dead-code panels, git insights —
  bus factor / churn / commit categories / contributors — architecture modules,
  packages, communities, entry points, execution flows, and a module
  **dependency graph**), **Wiki**, **Search**, **Risk**, **Hotspots**, **Dead
  code**, **Graph** (visual module graph), **Symbols**, **Decisions**, and
  **Settings** (per-repo health-check exclusions + delete).

Every panel is backed by an `/api/repos/{id}/...` endpoint.

## Local development

For live-reload while editing the UI, run the Vite dev server — the one case
where a second process is involved:

```bash
make ui-dev      # Vite on :5173, proxies /api and /mcp to the daemon on :7337
```

Keep a daemon running (`vor daemon start`) so the dev server has data to proxy
to. When done, fold your changes back into the binary:

```bash
make ui          # rebuild ui/dist
make install     # rebuild + install the binary (embeds the new dist)
vor daemon restart
```

## Make targets

| Target         | Does                                                        |
| -------------- | ---------------------------------------------------------- |
| `make ui`      | `npm install && npm run build` → `ui/dist`                  |
| `make ui-dev`  | Vite dev server (`:5173`), proxying `/api` + `/mcp`         |
| `make build`   | build the Go binaries (embeds the committed `ui/dist`)      |
| `make all`     | `make ui` then `make build`                                 |
| `make install` | build + install to `$GOBIN` (fresh-inode copy)              |
