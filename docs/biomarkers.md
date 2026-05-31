# vor — Code-Health Biomarkers

Reference for the biomarkers the health engine emits. Each is a deterministic
check run over parser + git + graph output; a hit becomes a row in
`health_findings` and drags the file's score.

> **Source of truth:** this doc mirrors `health.Biomarkers()` in
> [`internal/analysis/health/biomarkers.go`](../internal/analysis/health/biomarkers.go),
> which is served live at `GET /api/biomarkers` and powers the dashboard's
> Metrics reference page. If the two ever disagree, **the code wins** — see
> [Keeping this in sync](#keeping-this-in-sync).
>
> Planned additions are tracked in
> [biomarker-expansion-plan.md](biomarker-expansion-plan.md).

---

## How scoring works

Every file starts at a health **score of 10** (perfect). Each finding on that
file carries a `HealthImpact` (0–10) that is subtracted, then the result is
clamped to `[1, 10]`:

```
score = clamp(10 − Σ finding.HealthImpact, 1, 10)
```

Severity (`critical` / `high` / `medium` / `low`) classifies urgency;
`HealthImpact` is the separate, capped contribution to the score. Findings are
exposed via `vor_health_findings` (MCP), `GET /api/repos/{id}/health/findings`
(HTTP), and diffed across commits by `vor_health_diff`.

Any biomarker can be suppressed per-path or globally via the `health_rules`
config (see [configuration.md](configuration.md)) — useful when a check is
noisy for a given repo (e.g. duplication in generated code).

---

## Biomarkers by scope

### Function-level

#### `high_complexity`
- **Flags:** a function with too many branching paths (high cyclomatic
  complexity) — hard to test and reason about.
- **How:** cyclomatic complexity = 1 + the number of decision points
  (`if` / `for` / `case` / `&&` …) in the function.
- **Thresholds:** ≥10 = medium, ≥20 = high, ≥50 = critical.
- **Severities:** critical, high, medium.

#### `long_function`
- **Flags:** a function that runs on too long, usually doing several jobs that
  should be split apart.
- **How:** lines of code spanned by the function (last line − first line + 1).
- **Thresholds:** ≥60 lines = medium, ≥120 lines = high.
- **Severities:** high, medium.

#### `deep_nesting`
- **Flags:** code nested many blocks deep, which raises cognitive load and
  hides the control flow.
- **How:** the maximum control-flow nesting depth inside the function, measured
  by the parser.
- **Thresholds:** depth ≥4 = medium, ≥6 = high.
- **Severities:** high, medium.

#### `brain_method`
- **Flags:** a single function that is complex AND deeply nested AND long all
  at once — the worst "does-too-much" smell.
- **How:** flagged only when all three axes exceed their thresholds
  simultaneously.
- **Thresholds:** complexity ≥10 AND nesting ≥3 AND ≥50 lines. Always high.
- **Severities:** high.

#### `long_parameter_list`
- **Flags:** a function taking many parameters — often a sign the arguments
  should be grouped into an object.
- **How:** top-level parameter count parsed from the function signature.
- **Thresholds:** ≥5 parameters = medium, ≥8 = high.
- **Severities:** high, medium.

#### `feature_envy`
- **Flags:** a method that calls one external type more than its own — it may
  belong on that other type.
- **How:** calls grouped by receiver (excluding self, the standard library,
  and test helpers); flagged when one external receiver dominates.
- **Thresholds:** ≥4 calls to one external receiver accounting for ≥50% of the
  method's calls. Always medium.
- **Severities:** medium.

### Class-level

#### `god_class`
- **Flags:** a class or struct with too many methods — a sign of bloated design
  that violates single responsibility.
- **How:** count of methods whose parent is the class/struct/interface/trait/
  impl.
- **Thresholds:** ≥15 methods = medium, ≥25 = high.
- **Severities:** high, medium.

#### `deep_inheritance`
- **Flags:** a type buried deep in an inheritance chain — fragile, hard to
  reason about, and tightly bound to its ancestors.
- **How:** depth of inheritance tree (longest ancestor chain) built from
  heritage relations across all files. Fires only where inheritance exists.
- **Thresholds:** depth ≥4 = medium, ≥6 = high.
- **Severities:** high, medium.

### File-level

#### `duplication`
- **Flags:** a block of code repeated verbatim in several places — copy-paste
  that likely belongs in one shared function.
- **How:** Rabin–Karp rolling-hash fingerprinting over normalized 6-line
  windows; matching windows across the repo are coalesced into duplicate
  clusters.
- **Thresholds:** by cluster size — ≥6 sites = high, ≥3 = medium, otherwise
  low. Clusters spanning ≥8 sites are treated as boilerplate and ignored.
- **Severities:** high, medium, low.

#### `untested_hotspot`
- **Flags:** a frequently-changed (high-churn) file that lacks adequate test
  coverage — high regression risk.
- **How:** files flagged as git hotspots whose imported line coverage is below
  threshold, or that have no paired test file by language convention.
- **Thresholds:** hotspot file with coverage <50% (or no test file at all).
  Always high.
- **Severities:** high.

#### `hidden_coupling`
- **Flags:** two files that change together often but have no explicit
  import/call link — latent coupling that should be made visible.
- **How:** historical co-change pairs that have no dependency edge in the graph
  in either direction.
- **Thresholds:** a co-change pair with no dependency edge. Always medium.
- **Severities:** medium.

#### `shotgun_surgery`
- **Flags:** a file that co-changes with many others — edits here tend to
  ripple across the codebase.
- **How:** count of distinct files this one historically co-changes with.
- **Thresholds:** ≥8 co-changing files. Always medium.
- **Severities:** medium.

#### `god_file`
- **Flags:** a file declaring too many top-level symbols — module bloat, the
  file-level analogue of a god class.
- **How:** count of top-level symbols (free functions and type declarations,
  excluding their methods and bare constants/variables). A file built around a
  single large class counts as one and is left to `god_class` instead.
- **Thresholds:** ≥20 top-level symbols = medium, ≥40 = high.
- **Severities:** high, medium.

---

## Signal inputs

Biomarkers draw on four signal sources, all produced earlier in the pipeline:

| Source | Examples used today |
|---|---|
| Parser (per symbol) | complexity, nesting depth, line span, signature, receiver of each call |
| Parser (per file)   | symbol list, test/entry-point flags, language |
| Git intelligence    | hotspot flag, co-change pairs |
| Graph               | dependency edges (for `hidden_coupling`) |
| Coverage (imported) | line coverage % (for `untested_hotspot`) |

Several extracted signals — graph centrality (`InDegree`/`OutDegree`/
`PageRank`), per-file git (`BusFactor`/`ChurnPercentile`/`AgeDays`), and symbol
metadata (`Docstring`/`Visibility`/`Heritage`) — are **not yet** consumed by
any biomarker. The [expansion plan](biomarker-expansion-plan.md) proposes
biomarkers that use them.

---

## Keeping this in sync

`health.Biomarkers()` is the authoritative catalog; this file is a
human-readable mirror. To prevent drift, the recommended follow-up is a
**golden-file test** in `internal/analysis/health/` that renders
`Biomarkers()` to markdown and asserts it equals this file (regenerating with
`-update`), so adding a biomarker in code without updating this doc fails CI.
Until that exists, update both together when changing the roster.
