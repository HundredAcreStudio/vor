# vor — Biomarker Expansion Plan

Design doc for the next wave of code-health biomarkers. Addresses the
"~6 more biomarkers" / "deeper biomarkers" gap tracked in
[ROADMAP.md](ROADMAP.md).

> **Status:** Phase A (Tier 1) implemented — `god_file` and `deep_inheritance`
> shipped in `internal/analysis/health/biomarkers_tier1.go` with tests. The
> third proposed Tier-1 marker, `undocumented_api`, was **dropped**: it depends
> on per-symbol docstrings the parsers don't extract (only Python captures a
> module-level docstring today), and "undocumented exported symbol" is a
> surface-quality lint that external tools (golangci-lint, ESLint) already do
> well — the right home for it is SARIF ingestion + fusion (see §6), not a
> built-in biomarker. Phases B and C (Tiers 2–3) still pending.
> Author context: the engine now ships 12 biomarkers
> (`internal/analysis/health/`). This doc proposes 5 more beyond Phase A.

---

## 1. Motivation

The current roster uses only a fraction of the signal the pipeline already
extracts. Specifically, three rich signal sources are computed every run but
feed **no biomarker**:

- **Graph metrics** — `InDegree`, `OutDegree`, `PageRank`, `CommunityID`,
  `Betweenness` per node (`internal/ingestion/graph/graph.go`).
- **Per-file git intelligence** — `BusFactor`, `ChurnPercentile`, `AgeDays`,
  `CommitCount{Total,90d,30d}`, `ContributorCount`, line churn
  (`internal/ingestion/git/git.go` → `PerFile`). Health currently consumes
  only `HotspotPaths` and `CoChangePairs` derived from this.
- **Symbol metadata** — `Docstring`, `Visibility`, `IsExportedSymbol`,
  `Heritage` relations (`internal/ingestion/models/models.go`).

The largest *coverage* gap: `god_class` only fires on
`class/struct/interface/trait/impl`, so in Go, C, and procedural Python the
unit of bloat — the **file/module** — is invisible. And the entire
architectural surface (fan-in/out, cycles, centrality) is unmeasured.

### Design principles

1. **Universal first** — prioritize smells that fire across procedural, OO,
   and functional code, not just class-based languages.
2. **Lean into fusion** — a linter can flag complexity; only Vor can flag
   *complexity × churn × bus-factor*. The behavioral/architectural biomarkers
   are the differentiation.
3. **Reuse extracted signals** — every biomarker below uses data the pipeline
   already produces. Cost is detection logic + threading a signal into
   `Analyzer`, never new tree-sitter extraction.
4. **Zero schema cost** — `health_findings.biomarker_type` is free text and
   `details_json` is open, so there is **no DB migration and no MCP/HTTP
   change**. New biomarkers flow through `vor_health_findings`,
   `vor_health_diff`, and the dashboard automatically.

### Scoring policy (decided)

New biomarkers ship **on by default with small, capped health impact**
(0.5–2.0, in line with the existing `shotgun_surgery` 1.0–3.0 and
`hidden_coupling` 1.5). The `Exclude` / `health_rules` mechanism already lets
any biomarker be disabled per path or globally, so noisy-in-context markers
(e.g. `undocumented_api` on an application vs. a library) are opt-out-friendly
without special-casing. `undocumented_api` is the one near-zero-impact
exception — it is a surface-quality signal, not a structural one, and should
not tank scores.

---

## 2. The roster (8 biomarkers, 3 tiers by wiring cost)

### Tier 1 — No new `Analyzer` wiring

Signals already present on `models.ParsedFile` inside `Analyze(files)`.

| ID | Scope | Detection (existing signals) | Local/Global |
|---|---|---|---|
| `god_file` | file | top-level `len(pf.Symbols)`, max `EndLine` (NLOC proxy), `FileInfo.SizeBytes` | file-local |
| `undocumented_api` | function/class | `Visibility==public` or `IsExportedSymbol` **and** `Docstring==nil` | file-local |
| `deep_inheritance` | class | heritage chain depth built from `pf.Heritage` across all files | global |

- **`god_file`** — catches module bloat where `god_class` never fires (Go/C/
  Python). The single biggest coverage gap. Default thresholds: top-level
  symbols ≥ 20 medium, ≥ 40 high. Does not double-count `god_class`.
- **`undocumented_api`** — public surface lacking docs, every language.
  Gate to `KindFunction/Method/Class/Struct/Interface` that are exported, in
  non-test files, spanning more than a few lines, to skip trivial accessors.
  Severity low; **~zero health impact** per the scoring policy.
- **`deep_inheritance`** — Chidamber–Kemerer DIT. Default DIT ≥ 4 medium,
  ≥ 6 high. Fires only when inheritance exists → degrades gracefully for
  non-OO code. Global pass (chains cross files) but reads only `Heritage`
  already on the parsed files — no new `Analyzer` field.

### Tier 2 — Needs graph metrics threaded into `Analyzer`

Add a field, e.g. `GraphMetrics map[string]NodeMetrics{InDegree, OutDegree,
PageRank, CommunityID}`, populated in `PhaseHealth` from the graph phase
(metrics already computed in `graph.go`).

| ID | Scope | Detection | Local/Global |
|---|---|---|---|
| `unstable_dependency` | file | `OutDegree ≥ N` — too many outgoing deps (efferent coupling) | global |
| `cyclic_dependency` | file | SCC over directed import edges; file in a cycle of size ≥ 2 | global |
| `architectural_bottleneck` | file | high `InDegree`/`PageRank` **and** (high churn **or** high complexity) | global |

- **`unstable_dependency`** — Martin's instability metric. Fragile, hard-to-
  change files. Default `OutDegree` ≥ 20 medium, ≥ 40 high.
- **`cyclic_dependency`** — needs **directed** import edges; today
  `Analyzer.GraphEdges` is an undirected `bool` map. Add a directed
  import-edge set (filter graph edges by `EdgeImports`). Severity scales with
  cycle size.
- **`architectural_bottleneck`** — fusion: a widely-depended-on file that also
  keeps changing or is complex = systemic risk. Pure Vor signal.

### Tier 3 — Needs per-file git data threaded into `Analyzer`

Add `GitByFile map[string]git.PerFile` (or a trimmed subset), populated in
`PhaseHealth` from existing git-phase output.

| ID | Scope | Detection | Local/Global |
|---|---|---|---|
| `change_magnet` | file | file max `ComplexityEstimate` in top quartile **and** `ChurnPercentile ≥ 0.8` | global |
| `knowledge_silo` | file | `BusFactor == 1` **and** (`IsHotspot` or high `InDegree`/`PageRank`) | global |

- **`change_magnet`** *(flagship)* — the CodeScene "hotspot": high complexity ×
  high churn = the #1 refactoring priority. Reframes the score around *where
  refactoring pays off* rather than raw complexity. The headline addition.
- **`knowledge_silo`** — important code owned by one person; org/continuity
  risk no linter can see.

---

## 3. Per-biomarker implementation checklist

Uniform pattern, mirroring the existing engine:

1. Add the constant in `health.go` and to `AllBiomarkers()`.
2. Add threshold fields to `Thresholds` + values in `DefaultThresholds()`.
3. Write a detection fn returning `(bool, Severity)` plus an `…Impact` fn
   (mirror `complexityHit` / `godClassHit`).
4. Emit a `Finding` with calibrated (small, capped) `HealthImpact` and a
   populated `Details` map.
5. Register in `fileLocalBiomarkers` **only if** it depends on a single file's
   parse (`god_file`, `undocumented_api`). Graph- and git-derived markers stay
   **global** — correct for `AnalyzeIncremental`, which always recomputes
   global findings.
6. Add a `BiomarkerInfo` entry in `biomarkers.go` (drives the dashboard
   reference page and tooltips).
7. Tier 2/3 only: thread the new signal into `Analyzer` in `PhaseHealth`
   (`internal/pipeline/pipeline.go`).
8. Tests mirroring `biomarkers_phase14_test.go` / `health_test.go`.

No `health_findings` / `health_file_metrics` migration is required.

---

## 4. Score-impact calibration (risk)

Adding 8 file-level biomarkers risks swamping `fileMetric`'s `10 − Σimpact`
score (`duplication` alone already emits 357 findings on this repo).
Mitigations:

- Keep file-level impacts small (0.5–2.0) and capped.
- `undocumented_api` ≈ 0 impact (surfacing only).
- Lean on the existing `Exclude` / `health_rules` opt-out for context-noisy
  markers rather than hard-coding suppression.
- Validate thresholds against a handful of real repos before finalizing the
  defaults in `DefaultThresholds()`.

---

## 5. Sequencing

- **Phase A (Tier 1):** `god_file`, `undocumented_api`, `deep_inheritance` —
  no pipeline wiring, immediate value, lowest risk. Ship first and use it to
  validate score calibration.
- **Phase B (Tier 2):** thread graph metrics → `unstable_dependency`,
  `cyclic_dependency`, `architectural_bottleneck`.
- **Phase C (Tier 3):** thread per-file git → `change_magnet`,
  `knowledge_silo`.

---

## 6. Deliberately deferred

- **`bumpy_road`, complex-conditional, dead-parameter** — need per-block
  nesting or dataflow the parser doesn't expose (only `NestingDepth` *max*).
  Require new extraction.
- **`defect_density`** (per-file fix-commit ratio) — commit categories are
  repo-level today, not per-file. Revisit if per-file commit categorization
  is added.
- **`commented_out_code`** — doable via `SourceLoader` but noisy and
  comment-syntax-specific; lower ROI.
- **Linting ingestion** — tracked separately; the conclusion there is to
  ingest external linter output (SARIF) as its own dimension and fuse it
  rather than reimplement per-language linters.
