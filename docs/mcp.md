# MCP tools

Vor exposes its indexed intelligence as MCP tools so AI coding agents can query
the same data the dashboard shows — in one call, instead of grepping and
reading dozens of files. HTTP routes and MCP tools share one read layer
(`internal/insights`), so the surfaces stay in lockstep.

## Connecting

Two transports, both from the same binary:

- **Streamable HTTP** — the running daemon serves MCP at `http://<host>:7337/mcp`.
  One daemon, many clients, many repos (each tool call can target a repo by id
  or path via the `repo` argument).
- **stdio** — `vor mcp --repo <path>` for editors that spawn a child process per
  repo.

The easiest setup is the bundled **Claude Code plugin** (`plugins/claude-code/`):
it registers the `vor` MCP server and ships skills that get the agent to reach
for the tools (see below).

## Tool surface

Orientation:

- `vor_status` — repo summary (counts, hotspots, health average).
- `vor_attention` — prioritized "what should I look at" digest (knowledge silos, churn hotspots, dead code, decisions to review).
- `vor_get_architecture_diagram` — module map + inter-module deps + entry points (`format=mermaid` optional).
- `vor_languages`, `vor_packages`, `vor_modules` — tech-stack, package layout, per-module breakdown.

Before editing:

- `vor_risk` — modification risk for target files: blast radius (dependents), co-change partners, ownership/bus-factor, governing decisions, derived risk level.

Understanding:

- `vor_get_context` — triage card for files/symbols (summary, hotspot, decisions, child symbols).
- `vor_get_answer` — synthesized, cited answer to a natural-language question.
- `vor_get_why` — the architectural decision behind why code is shaped a way.
- `vor_search`, `vor_symbol` — find symbols / inspect one.

Graph & tracing:

- `vor_get_dependency_path`, `vor_get_execution_flows`, `vor_get_community`, `vor_callers`, `vor_dependents`, `vor_dependency_matrix`, `vor_entry_points`.

Health, git & quality:

- `vor_health`, `vor_health_findings`, `vor_hotspots`, `vor_dead_code`, `vor_decisions`, `vor_git_insights`, `vor_commit_categories`, `vor_externals`, `vor_security`, `vor_security_scan`.

Docs & ops:

- `vor_pages`, `vor_page`, `vor_pipeline_log`.

(Run `tools/list` against the server for the authoritative, current set.)

## Getting agents to use the tools

Two reinforcing mechanisms:

1. **Generated `CLAUDE.md`.** `vor init` / `vor claude-md` write a managed
   section into the repo's `CLAUDE.md` that every agent reads. It opens with
   proactive directives — *call `vor_risk` before editing; `vor_get_context` to
   orient; `vor_get_why` before refactors; `vor_get_answer` for how-does-X* —
   plus a tool table. This works in any indexed repo, no plugin install needed.

2. **Claude Code plugin** (`plugins/claude-code/`). Registers the `vor` MCP
   server (`.mcp.json`) and ships two auto-activating skills:
   - `pre-modification` — before editing, call `vor_risk`; warn on high risk.
   - `codebase-exploration` — use `vor_status` / `get_architecture_diagram` /
     `attention` / `get_answer` / `get_why` to orient before reading source.
