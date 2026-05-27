# Testing Plan — Review checklist

Use this to walk through 29 commits of work without missing anything important. Each section has commands to run and what to expect.

## 0. Prerequisites

```bash
go version          # 1.24+
which sqlite3       # for one DB inspection step (optional)
make build          # produces bin/repowise + bin/repowise-augment
```

If `make build` fails, stop here — most things won't work without cgo + tree-sitter compiling correctly. Expected: ~30s first build.

---

## 1. Automated test suite (5 minutes)

```bash
go test ./...
```

**Expect**: every package passes. ~30 test packages, no failures.

If anything fails, that's a bug — please flag it. The CI mindset I aimed for: any regression in any package should fail this single command.

For deeper confidence:
```bash
go test -race ./...    # data-race check; pipelinestore + http use goroutines
go test -count=2 ./... # flush caches, re-run to catch order-dependence
```

---

## 2. Read PORTING_PLAN.md + README.md (5 minutes)

Skim the **README status table** — that's the authoritative "what works today" view I committed in `docs(readme): update to reflect what's actually shipped`. If anything looks wrong vs reality, flag it.

PORTING_PLAN.md is the original roadmap from the start of this session — it hasn't been updated as phases progressed, so treat it as the **plan**, not the **status**.

---

## 3. Git log walkthrough (10 minutes)

```bash
git log --oneline initial..HEAD
```

29 commits, each titled by phase. Each commit message has detailed "what + why" body — read at least the messages for any commit whose title sounds load-bearing. Recommended read order:

1. `feat(ingestion): Phase 2 — traverser + Go/Python/TypeScript parsers` (the foundation)
2. `feat(ingestion): Phase 2 Pass D — dependency graph + persistence` (the core value proposition)
3. `feat(git): Phase 3 Pass A — per-file git intelligence` (hotspots, ownership)
4. `feat(providers): Phase 4 Pass A — LLM provider abstraction` (interface design)
5. `feat(server): Phase 8 Pass A — HTTP API over the analysis output` (route shape)
6. `feat(mcp): Phase 8 Pass B — MCP server with 5 tools over stdio` (MCP design)
7. `feat(pipeline): Phase 7 Pass A — tracked orchestrator + 'repowise init'` (state machine)

If any commit's stated intent doesn't match the diff, please flag it — that's the most likely place I made a wrong call.

---

## 4. End-to-end CLI smoke test (10 minutes)

Use any real-world repo you have handy. Examples below use `~/projects/some-repo`.

```bash
# 1. Doctor — confirm environment is OK before doing real work.
./bin/repowise doctor --repo ~/projects/some-repo

# 2. Init — full tracked pipeline run.
./bin/repowise init ~/projects/some-repo

# 3. Status — one-screen summary of what landed.
./bin/repowise status --repo ~/projects/some-repo

# 4. Each read-only view.
./bin/repowise health    --repo ~/projects/some-repo --limit 10
./bin/repowise dead-code --repo ~/projects/some-repo --safe-only
./bin/repowise hotspots  --repo ~/projects/some-repo --limit 10
./bin/repowise search    SomeSymbol --repo ~/projects/some-repo
./bin/repowise externals --repo ~/projects/some-repo
./bin/repowise costs     --repo ~/projects/some-repo
./bin/repowise pipeline log --repo ~/projects/some-repo
./bin/repowise pipeline summary --repo ~/projects/some-repo
```

**What to check**:
- Init completes through 8 phases (`traverse → parse → git → graph → deadcode → health → externals → persist`). Phase durations should all be < 1s on small repos, < 30s on huge ones.
- `status` numbers should make sense: graph nodes ≈ files + symbols; externals match the repo's manifests; dead-code count reflects unreachable files.
- `health` worst-scoring files should be ones you intuitively know are messy.
- `hotspots` should match your gut feeling about which files churn most.
- `search` should rank highly-imported symbols first.
- `pipeline log` shows the 8 phases as `completed`.

Try **negative cases** too:
```bash
./bin/repowise init /nonexistent      # should fail clean
./bin/repowise search ZzZNeverFinds   # should print "no matches"
./bin/repowise dead-code --repo /tmp  # empty DB should print "run init first"
```

---

## 5. HTTP server surface (10 minutes)

```bash
./bin/repowise serve --repo ~/projects/some-repo --addr 127.0.0.1:7337 &
SERVER_PID=$!
sleep 1

# Service health
curl -s http://127.0.0.1:7337/api/health | jq .

# Find the repo ID:
REPO=$(curl -s http://127.0.0.1:7337/api/repos | jq -r '.repos[0].id')

# Every endpoint:
curl -s "http://127.0.0.1:7337/api/repos/$REPO"                          | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/graph/nodes?limit=5"      | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/graph/edges?type=imports" | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/hotspots"                 | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/dead-code?safe_only=true" | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/health"                   | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/health/findings?limit=5"  | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/health/files?order=score" | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/externals?ecosystem=npm"  | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/search?q=Foo"             | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/symbol?symbol_id=path/to/foo.go::Foo" | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/callers?symbol_id=path/to/foo.go::Foo" | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/dependents?file_path=path/to/foo.go"  | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/pipeline/log"             | jq .
curl -s "http://127.0.0.1:7337/api/repos/$REPO/pipeline/summary"         | jq .

# Negative case
curl -i http://127.0.0.1:7337/api/repos/no-such-id        # → 404
curl -i http://127.0.0.1:7337/api/repos/$REPO/symbol      # → 400 (missing q)

kill $SERVER_PID
```

**What to check**:
- Every endpoint returns 200 with non-empty JSON for real repos.
- Negative cases return correct 4xx codes with structured error bodies.
- Structured slog lines appear on stderr for every request.
- Server shuts down cleanly on SIGINT (no goroutine leaks; if you want to confirm, run with `GODEBUG=gctrace=1`).

---

## 6. MCP server (5 minutes)

The cleanest way to verify MCP is to wire it into Claude Code.

```bash
# Test against your actual code via Claude Code:
# Add to ~/.claude/.mcp.json or your project's .mcp.json:
{
  "servers": {
    "repowise": {
      "command": "/abs/path/to/bin/repowise",
      "args": ["mcp", "--repo", "/abs/path/to/some-repo"]
    }
  }
}
```

Then in Claude Code, ask:
- "List all tools the repowise MCP server provides" — should show 11
- "Use repowise_status to summarize this repo"
- "What are the top 5 hotspots?"
- "Find any dead code that's safe to delete"
- "Search for SomeFunctionName"

For a faster local check without Claude Code, the test suite already drives every tool via `HandleMessage`. So `go test ./internal/server/mcp/` validates the actual JSON-RPC plumbing.

---

## 7. Architecture / structural review (15 minutes)

Reading the code carefully matters at least as much as running it. Suggested order, from foundation up:

```bash
tree -L 3 internal/ -I 'queries|testdata'
```

| Read these files | To check |
|---|---|
| `internal/ingestion/models/models.go` | The shared types — every other package depends on these |
| `internal/ingestion/languages/languages.go` | Language registry — do supported language coverage / extensions match your needs? |
| `internal/ingestion/parser/parser.go` | Parser registry design — interface vs concrete types |
| `internal/ingestion/parser/common/common.go` | Helpers shared by 9 parsers — small surface |
| `internal/ingestion/parser/golang/golang.go` | One full per-language parser — the others mirror this structure |
| `internal/persistence/migrations/sqlite/0001_init.sql` | 28-table schema — sanity-check the column types you'll query against |
| `internal/persistence/db/db.go` | Driver selection — URL forms, PRAGMAs applied |
| `internal/ingestion/graph/graph.go` + `builder.go` + `metrics.go` | Graph build — multi-edge handling, 3-tier call resolver, metrics adapter |
| `internal/ingestion/git/git.go` | Git intelligence — hotspot scoring, bus factor, co-change |
| `internal/analysis/health/health.go` | 7 biomarkers — threshold tunability |
| `internal/analysis/deadcode/deadcode.go` | Dead-code analyzer — confidence model |
| `internal/providers/provider.go` | LLM interface — does the shape match what real providers need? |
| `internal/pipeline/pipeline.go` | Orchestrator — phase order + error policy |
| `internal/server/http/server.go` + `routes/*.go` | HTTP shape — pagination + filter conventions |
| `internal/server/mcp/{mcp,tools,handlers}.go` | MCP tool shape |
| `internal/cli/commands/*.go` | CLI surface — flag naming consistency |

Things I'd specifically like a second opinion on:
- **Schema choices**: 28 tables felt right, but I collapsed all 24 alembic migrations into one consolidated `0001_init.sql`. If you ever want to ingest a Python `.repowise/wiki.db`, this is incompatible by design.
- **Multi-edge graph**: I store typed edges in our own slice/map instead of `gonum.multi.DirectedGraph`. Read `internal/ingestion/graph/graph.go` and decide if this is the right call.
- **Health analyzer growing fields**: `Analyzer.HotspotPaths`, `CoChangePairs`, `GraphEdges` got added one at a time. At some point this should become a single `Inputs` struct. Flag if you'd prefer to refactor now.
- **Pipeline phase order**: `traverse → parse → git → graph → deadcode → health → externals → persist`. Git runs before graph so its hotspots can flow into health. Persist is last so a failure mid-run doesn't leave partial state.

---

## 8. Tests I want a second opinion on

The most valuable tests are the **integration tests that hit real SQLite + real tree-sitter + real go-git**. Read these to judge if they cover the right cases:

```bash
internal/persistence/migrations/migrations_test.go    # schema roundtrip
internal/ingestion/git/git_test.go                    # real git repo in tmpdir
internal/persistence/graphstore/graphstore_test.go    # full snapshot persist + cascade
internal/server/http/server_test.go                   # every endpoint via httptest
internal/server/mcp/mcp_test.go                       # every tool via HandleMessage
internal/pipeline/pipeline_test.go                    # full orchestrator run
```

I deliberately added tests for things the Python source doesn't test (the README points to several). Flag any that feel like over-coverage or that test the wrong thing.

---

## 9. What I'd hit hardest

If you only have an hour to review, do this:

1. `go test ./...` — confirms nothing is broken (2 min)
2. `./bin/repowise init <your big repo>` — end-to-end smoke (5 min)
3. Read commit messages for Pass D / Pass A / MCP Pass B / Pipeline Pass A — the four most consequential design choices (10 min)
4. Read `internal/persistence/migrations/sqlite/0001_init.sql` — every other layer depends on this (10 min)
5. Read `internal/ingestion/graph/graph.go` + `builder.go` — the core value proposition (15 min)
6. Hit each MCP tool through Claude Code — the most user-visible surface (15 min)

The remaining time goes toward checking architecture decisions you care about.

---

# How I tracked what I was doing

Honest answer: **the system is patchy.** Here's what I actually used:

## Mechanisms I have

| Mechanism | What it tracks | Limitations |
|---|---|---|
| **TaskList (#1–#13)** | Phase-level: "Phase 2 in progress" | Coarse. I didn't add sub-tasks for passes within phases; the granularity is wrong for sub-pass work. |
| **PORTING_PLAN.md** | The original roadmap (library choices, phases, risks) | I haven't updated it as phases progressed — it's the **plan**, not the **status**. |
| **README.md status table** | Per-subsystem "what works today" | Updated once mid-session (commit `docs(readme): update to reflect what's actually shipped`). Drifts with every new commit. |
| **Commit messages** | Per-pass detailed log of what + why | The **actual** source of truth. Every pass has a multi-paragraph body explaining design choices. `git log --oneline` is the index. |
| **Code itself** | What's actually registered/wired | Registered parsers, mounted routes, MCP tools live as `init()` calls / `AddCommand` calls — counting them tells you the surface area. |

## What I lean on most

For "what's done": **commit messages**. They're the audit trail. The task list was too lossy to capture sub-pass work; I made up the "Pass A / B / C / D / E" terminology *during* commits to give myself granularity the task list lacked.

For "what's left": **PORTING_PLAN.md §5 (Phased Roadmap)** plus my own memory of which Passes I labeled DONE / DEFERRED in commit bodies. I summarized the remaining work in the "checkpoint" messages I wrote between batches.

For "is this in a coherent state": **the test suite**. If `go test ./...` passes and `./bin/repowise init <repo>` runs through, the system has internal consistency even if I can't recite the inventory.

## What I'd do differently

If I were starting over, I'd:

1. **Track per-Pass tasks, not per-Phase.** Each phase has 2–7 passes; the task list should mirror that granularity. (TaskCreate#3, "Phase 1 — Persistence layer," is too coarse to be useful — it covers schema design, the migration runner, repository CRUD, AND graphstore which actually lives in Phase 2.)
2. **Update README + PORTING_PLAN at every commit**, not periodically. Drift compounds.
3. **Add a `STATUS.md` that's the canonical "what's live"**. The README serves this purpose now but I treat it more like marketing.
4. **Number my commits by phase + pass** in the title so `git log --oneline` is self-indexing. Current titles do this, but inconsistently (`feat(ingestion): Phase 2 Pass E` vs `feat(server): add symbol / callers / dependents...`).

## What's reliable

- **`git log` is correct.** Every commit message is an honest description of what shipped.
- **The code itself doesn't lie.** Count `parser.Register` calls (9), count `root.AddCommand` calls (16), count `s.srv.AddTool` calls (11). The surfaces are what they are.
- **The test suite is the spec.** If you want to know what behavior I promised, read the tests.

## What you should NOT trust

- **The TaskList**. It's stale — six phases are "in_progress" simultaneously, which means nothing useful.
- **PORTING_PLAN.md's "Status" section** (if I added one — I don't think I did). The original plan is fine; any "this is shipped" assertions in it would be untrustworthy.
- **My session summaries**. They're best-effort recaps, not audits. The commit log supersedes any summary I wrote.

If you ever want to validate "is what Claude says shipped *actually* shipped," run:
```bash
git log --oneline initial..HEAD              # what claims to be shipped
go test ./...                                # what actually works
grep -r "parser.Register\|root.AddCommand\|s.srv.AddTool" internal/  # what's wired
```

Those three commands disagree → there's a bug. They agree → trust them over me.
