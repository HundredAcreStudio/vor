# Configuration

Configuration lives in the **database**, not in YAML files. Two things can't
come from the DB and are resolved first, from the environment and built-in
defaults (this is "bootstrap"):

- the **database URL** itself, and
- **provider API keys** (kept out of the DB so they're never persisted).

Everything else resolves from the `settings` table once the DB is open.

## Resolution order

```
built-in defaults
  → global settings   (settings rows with repository_id = '')
  → per-repo settings (settings rows with repository_id = <repo>)
  → VOR_* environment variables   (highest precedence)
```

Per-repo settings override global ones; environment variables override both, so
CI/Docker can force a value without touching the DB.

## The database URL (bootstrap)

Resolved before anything else, from:

1. `VOR_DB_URL` (or `VOR_DATABASE_URL`) — e.g. `sqlite:/path/to.db` or
   `postgres://user@host/db`, otherwise
2. the global SQLite file `~/.config/vor/vor.db`
   (`$XDG_CONFIG_HOME/vor/vor.db`), created on first use.

There is no per-repo database — one global DB holds every repo.

## Provider API keys (env only)

Read from the environment, never stored:

| Variable                            | Provider  |
| ----------------------------------- | --------- |
| `ANTHROPIC_API_KEY`                 | Anthropic |
| `OPENAI_API_KEY`                    | OpenAI    |
| `GEMINI_API_KEY` / `GOOGLE_API_KEY` | Google    |
| `OPENROUTER_API_KEY`                | LiteLLM   |

## DB-backed settings

These resolve from the `settings` table (global or per-repo), overridable by
the matching `VOR_*` env var:

| Key                | Env override          | Notes                                   |
| ------------------ | --------------------- | --------------------------------------- |
| `provider`         | `VOR_PROVIDER`        | anthropic / openai / google / ollama / litellm / mock |
| `model`            | `VOR_MODEL`           |                                         |
| `embedder`         | `VOR_EMBEDDER`        |                                         |
| `embedding_model`  | `VOR_EMBEDDING_MODEL` |                                         |
| `embedding_dims`   | `VOR_EMBEDDING_DIMS`  |                                         |
| `host` / `port`    | `VOR_HOST` / `VOR_PORT` | daemon bind address                   |
| `log_level`        | `VOR_LOG_LEVEL`       |                                         |
| `rpm` / `tpm`      | `VOR_RPM` / `VOR_TPM` | LLM rate limits                         |
| `languages`        | `VOR_SKIP_LANGUAGES`  | enabled/skipped tree-sitter languages   |
| `watch`            | `VOR_WATCH` / `VOR_WATCH_DEBOUNCE` | daemon auto-reindex behavior |
| `reasoning`        | —                     | LLM-assisted decision mining            |
| `health_rules`     | —                     | code-health exclusions (below)          |

Values are JSON-encoded in the table. The HTTP settings API
(`GET/PUT/DELETE /api/repos/{id}/settings/{key}`) reads and writes them, which
is what the dashboard's per-repo **Settings** page uses.

## Code-health exclusions (`health_rules`)

Suppress biomarker findings for files matching a glob (`pattern`, gitignore
syntax) or a path prefix (`path`). `overrides` maps a biomarker name to an
action — `disabled` (also `off` / `skip` / `ignore`) turns that check off, and
the `"*"` key applies to every biomarker. Exclusions are **health-only**:
matched files still appear in the graph, search, and dead-code analysis; they
just stop generating health findings.

Edit them on the repo's **Settings** page in the dashboard, or PUT the
`health_rules` key directly. The JSON value:

```json
[
  { "pattern": "**/*_test.go", "overrides": { "high_complexity": "disabled", "long_function": "disabled" } },
  { "path": "internal/generated/", "overrides": { "*": "disabled" } }
]
```

Changes take effect on the next (re)index of the repo.

## User-global state

Besides the DB, the daemon records runtime state under XDG dirs:

| Path                              | Purpose                                            |
| --------------------------------- | -------------------------------------------------- |
| `~/.config/vor/vor.db`            | the global database (config + all indexed data)    |
| `~/.local/state/vor/daemon.json`  | the running `serve` instance (pid, addr, db url)   |
| `~/.local/state/vor/daemon.log`   | backgrounded daemon stdout/stderr (`vor daemon logs`) |
