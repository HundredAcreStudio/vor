package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// jsonUnmarshalLocal exposes encoding/json.Unmarshal under a name the
// handler file can call without re-importing json. Tiny shim because the
// handler file only needs unmarshal — keeping its imports clean.
var jsonUnmarshalLocal = json.Unmarshal

// repoArgDesc is the shared text for the `repo` parameter, attached to
// every tool so a daemon serving multiple repos can address a specific one.
// Single-repo mode treats the argument as optional (falls back to the
// server's default RepositoryID).
const repoArgDesc = "Repo to query: full repository id or local filesystem path. Optional in single-repo mode (falls back to the configured default)."

// registerTools wires every tool onto s.srv. New tools should land here so
// the registration sits in one place and a follow-up can split this file
// per tool group when the count grows.
func (s *Server) registerTools() {
	s.srv.AddTool(mcp.NewTool(
		"vor_status",
		mcp.WithDescription("Repository-wide summary: graph size, hotspot count, dead-code count, average health score, external dependency totals. Use this as the first tool call to understand what's been indexed."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolStatus))

	s.srv.AddTool(mcp.NewTool(
		"vor_risk",
		mcp.WithDescription("Modification risk assessment — CALL THIS BEFORE EDITING a file, especially shared utilities, core modules, or files the user didn't name. For each target returns: git hotspot status + churn, how many files depend on it (blast radius) with the top dependents, co-change partners you may also need to touch, ownership + bus factor, the architectural decisions that govern it, and a derived risk level (high/medium/low) with reasons. Pure indexed data, no LLM. Batch multiple files into one call."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithArray("targets", mcp.Description("File paths to assess (e.g. 'internal/foo.go'). Multiple allowed in one call."), mcp.Items(map[string]any{"type": "string"})),
		mcp.WithString("target", mcp.Description("Single-target convenience alternative to `targets`.")),
	), s.wrap(s.toolRisk))

	s.srv.AddTool(mcp.NewTool(
		"vor_health_diff",
		mcp.WithDescription("Did my changes help or hurt? Compares the repo's CURRENT per-file code-health scores against the last committed snapshot and returns the net average delta plus the files that regressed or improved. Call after editing to check whether the change degraded the codebase before committing. Pure indexed data, no LLM."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolHealthDiff))

	s.srv.AddTool(mcp.NewTool(
		"vor_attention",
		mcp.WithDescription("A prioritized \"what should I look at\" digest for the repo: knowledge silos (single-owner active files), ungoverned churn hotspots, safe-to-delete dead code, and architectural decisions awaiting review. Call when onboarding to a repo or deciding where attention/cleanup is most warranted."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolAttention))

	// --- repo-level analytics (same data as the dashboard overview) ---
	s.srv.AddTool(mcp.NewTool(
		"vor_git_insights",
		mcp.WithDescription("Repo git analytics: bus-factor distribution (safe ≥3 / warning 2 / risk ≤1 contributors) with the highest-risk files, the churn-percentile distribution, and the top contributors by commit count. Use to judge ownership concentration and where change is concentrated."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolGitInsights))

	s.srv.AddTool(mcp.NewTool(
		"vor_commit_categories",
		mcp.WithDescription("How development effort splits across commit categories (feature / fix / refactor / docs / test / dependency / chore), from classifying commit subjects. Use to characterize what kind of work this repo sees."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolCommitCategories))

	s.srv.AddTool(mcp.NewTool(
		"vor_languages",
		mcp.WithDescription("Language distribution by indexed file count. Use to understand the repo's tech stack at a glance."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolLanguages))

	s.srv.AddTool(mcp.NewTool(
		"vor_modules",
		mcp.WithDescription("Per-module breakdown (by top two path segments): file and symbol counts, documentation coverage, and outgoing cross-module import dependencies. Use to see how the codebase is organized and which modules are under-documented or heavily coupled."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolModules))

	s.srv.AddTool(mcp.NewTool(
		"vor_packages",
		mcp.WithDescription("Packages detected from dependency manifests (with each package's language), and whether the repo is a monorepo. Use to learn the project's package layout."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolPackages))

	s.srv.AddTool(mcp.NewTool(
		"vor_dependency_matrix",
		mcp.WithDescription("Module×module import-density matrix: for the most-connected modules, how many import edges run from each module to each other. Use to spot tightly-coupled or central modules."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolDependencyMatrix))

	s.srv.AddTool(mcp.NewTool(
		"vor_entry_points",
		mcp.WithDescription("The repo's indexed entry-point files (mains, servers, CLI roots), ranked by PageRank. Use to find where execution starts before tracing flows."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolEntryPoints))

	// --- task-oriented synthesis tools ---
	s.srv.AddTool(mcp.NewTool(
		"vor_get_context",
		mcp.WithDescription("Triage card for one or more files/symbols — kind, name, language, generated summary, git hotspot bit, the decision_records that touch it, and child symbol_ids you can pipe into vor_symbol. Pure indexed data, no LLM. The cheapest way to orient on a target before reading source."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithArray("targets", mcp.Description("File paths or symbol_ids (e.g. 'internal/foo.go' or 'internal/foo.go::Bar'). Multiple allowed in one call."), mcp.Items(map[string]any{"type": "string"})),
		mcp.WithString("target", mcp.Description("Single-target convenience alternative to `targets`.")),
	), s.wrap(s.toolContext))

	s.srv.AddTool(mcp.NewTool(
		"vor_get_why",
		mcp.WithDescription("Architectural decision archaeology — WHY the code is shaped this way. Matches decision records (inline markers, ADRs, CHANGELOG, commit archaeology) by query and/or target file, then synthesises the rationale (when an LLM provider is configured) with the raw records attached for verification. Call before refactors or pattern divergences."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("query", mcp.Description("Free-text question, e.g. 'why JWT instead of sessions'.")),
		mcp.WithArray("targets", mcp.Description("File paths to scope the search to."), mcp.Items(map[string]any{"type": "string"})),
	), s.wrap(s.toolWhy))

	s.srv.AddTool(mcp.NewTool(
		"vor_get_answer",
		mcp.WithDescription("Synthesised answer to a natural-language question about the codebase, grounded in generated documentation + symbol references, with the citations it used and a calibrated confidence (high/medium/low). When no LLM provider is configured it returns the retrieved candidates as best_guesses instead. First call for 'how does X work' / 'where is Y handled'."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("question", mcp.Required(), mcp.Description("The natural-language question to answer.")),
	), s.wrap(s.toolAnswer))

	// --- graph-traversal tools (deterministic, no LLM) ---
	s.srv.AddTool(mcp.NewTool(
		"vor_get_community",
		mcp.WithDescription("Graph communities — clusters of files that are tightly interconnected (Louvain-style community_id from the graph build). With no args, returns the largest communities and their top files by PageRank. With `target`, returns every member of that file's community. Use to discover the natural module boundaries the import graph reveals."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("target", mcp.Description("A file path or node_id; returns the members of its community.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(20), mcp.Description("Max communities to list in survey mode (1–100).")),
	), s.wrap(s.toolCommunity))

	s.srv.AddTool(mcp.NewTool(
		"vor_get_dependency_path",
		mcp.WithDescription("Shortest dependency path between two nodes over imports/calls/defines edges. Answers 'how does A reach B?' — returns the node sequence and hop count, or found=false when no path exists. Both endpoints accept a file path or a node_id."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("from", mcp.Required(), mcp.Description("Source file path or node_id.")),
		mcp.WithString("to", mcp.Required(), mcp.Description("Target file path or node_id.")),
	), s.wrap(s.toolDependencyPath))

	s.srv.AddTool(mcp.NewTool(
		"vor_get_execution_flows",
		mcp.WithDescription("Outbound flow trees rooted at the repo's entry points (or an explicit `entry`), walking imports/calls to a bounded depth. Use to understand 'what runs when this entry point fires?'. Cycle-guarded; children capped for legibility."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("entry", mcp.Description("Explicit root file path or node_id; defaults to indexed entry points.")),
		mcp.WithNumber("max_depth", mcp.DefaultNumber(4), mcp.Description("Maximum traversal depth (1–8, default 4).")),
		mcp.WithNumber("limit", mcp.DefaultNumber(10), mcp.Description("Max entry-point roots to expand (1–50, default 10).")),
	), s.wrap(s.toolExecutionFlows))

	s.srv.AddTool(mcp.NewTool(
		"vor_get_architecture_diagram",
		mcp.WithDescription("Repo-level architecture map: the largest communities (with a representative file each), the weighted import dependencies between them, and the entry points. Pass format=mermaid for a renderable flowchart string alongside the structured data. The fastest way to orient on an unfamiliar repo's shape."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("format", mcp.Description("Set to 'mermaid' to also receive a mermaid flowchart string.")),
	), s.wrap(s.toolArchitectureDiagram))

	s.srv.AddTool(mcp.NewTool(
		"vor_hotspots",
		mcp.WithDescription("Files with high git churn. Returns the top N files by churn percentile, with owner, contributor count, and bus factor. Use this to find where a refactoring effort will have outsized impact."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithNumber("limit", mcp.DefaultNumber(20), mcp.Description("Maximum files to return (1–100, default 20).")),
	), s.wrap(s.toolHotspots))

	s.srv.AddTool(mcp.NewTool(
		"vor_dead_code",
		mcp.WithDescription("Unreachable files and symbols sorted by confidence. Optional safe_only filter returns only the high-confidence (≥0.9) findings flagged safe to delete."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithBoolean("safe_only", mcp.DefaultBool(false), mcp.Description("If true, only return findings flagged SafeToDelete.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum findings to return (1–500, default 50).")),
	), s.wrap(s.toolDeadCode))

	s.srv.AddTool(mcp.NewTool(
		"vor_health",
		mcp.WithDescription("Code health summary: average score (1–10), finding counts per biomarker, and the worst-scoring files."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithNumber("worst_limit", mcp.DefaultNumber(10), mcp.Description("Number of worst-scoring files to include (1–50, default 10).")),
	), s.wrap(s.toolHealth))

	s.srv.AddTool(mcp.NewTool(
		"vor_health_findings",
		mcp.WithDescription("Paginated code-health findings, ordered by severity then impact."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("biomarker", mcp.Description("Filter to one biomarker type (e.g. 'high_complexity').")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum findings to return (1–500, default 50).")),
	), s.wrap(s.toolHealthFindings))

	s.srv.AddTool(mcp.NewTool(
		"vor_security",
		mcp.WithDescription("Security scan findings (hardcoded secrets, weak crypto, likely injection sinks), ordered by severity. Populated by `vor security scan`. Secret values are redacted."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("severity", mcp.Description("Filter to a severity: critical|high|medium|low.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(100), mcp.Description("Maximum findings to return (1–500, default 100).")),
	), s.wrap(s.toolSecurity))

	s.srv.AddTool(mcp.NewTool(
		"vor_symbol",
		mcp.WithDescription("Detail for one symbol: kind, file path, line range, visibility, complexity, PageRank. Look up by the canonical symbol_id (e.g. 'src/foo.go::User::Save')."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("symbol_id", mcp.Required(), mcp.Description("Canonical symbol ID, formatted '<file>::<symbol>' or '<file>::<parent>::<symbol>'.")),
	), s.wrap(s.toolSymbol))

	s.srv.AddTool(mcp.NewTool(
		"vor_callers",
		mcp.WithDescription("Who calls this symbol? Returns the incoming 'calls' / 'has_method' edges from the dependency graph, with caller symbol ID and confidence."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("symbol_id", mcp.Required(), mcp.Description("Canonical symbol ID to find callers of.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum callers to return (1–500, default 50).")),
	), s.wrap(s.toolCallers))

	s.srv.AddTool(mcp.NewTool(
		"vor_dependents",
		mcp.WithDescription("Which files import the given file? Returns incoming 'imports' edges. Use to assess the blast radius of changing a file."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("file_path", mcp.Required(), mcp.Description("Repo-relative file path (e.g. 'pkg/foo/bar.go').")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum dependents to return (1–500, default 50).")),
	), s.wrap(s.toolDependents))

	s.srv.AddTool(mcp.NewTool(
		"vor_externals",
		mcp.WithDescription("Third-party dependencies declared in manifest files (package.json, pyproject.toml, Cargo.toml, go.mod, *.csproj). Optional ecosystem + dev filters."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("ecosystem", mcp.Description("Filter to one ecosystem: 'npm', 'pypi', 'cargo', 'go', or 'nuget'.")),
		mcp.WithBoolean("dev_only", mcp.DefaultBool(false), mcp.Description("If true, return only dev/test dependencies.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(200), mcp.Description("Maximum records to return (1–1000, default 200).")),
	), s.wrap(s.toolExternals))

	s.srv.AddTool(mcp.NewTool(
		"vor_search",
		mcp.WithDescription("Search the codebase. Default is a substring match over graph nodes (files + symbols) ranked by PageRank. Set semantic=true to rank wiki pages by embedding similarity instead (requires `vor embed` to have run; falls back to substring match otherwise)."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("query", mcp.Required(), mcp.Description("Substring to match against name / qualified_name / node_id, or a natural-language query when semantic=true.")),
		mcp.WithString("node_type", mcp.Description("Filter to 'file' or 'symbol' (lexical mode only).")),
		mcp.WithBoolean("semantic", mcp.DefaultBool(false), mcp.Description("Rank wiki pages by embedding similarity instead of substring matching node names.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(25), mcp.Description("Maximum matches to return (1–200, default 25).")),
	), s.wrap(s.toolSearch))

	s.srv.AddTool(mcp.NewTool(
		"vor_pipeline_log",
		mcp.WithDescription("Recent pipeline phase executions for this repository, newest first. Each entry is one row of pipeline_jobs (phase, state, started_at, error if failed). Useful for diagnosing 'why is my data out of date?'"),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithNumber("limit", mcp.DefaultNumber(20), mcp.Description("Maximum rows to return (1–200, default 20).")),
	), s.wrap(s.toolPipelineLog))

	s.srv.AddTool(mcp.NewTool(
		"vor_security_scan",
		mcp.WithDescription("Run the pattern-based security scanner over the repo and replace stored findings. Synchronous (regex over source — no LLM). Returns a severity breakdown; read individual findings with vor_security."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolSecurityScan))

	// Daemon registry control — only when a registrar (live watcher) is wired.
	if s.opts.Registrar != nil {
		s.srv.AddTool(mcp.NewTool(
			"vor_track",
			mcp.WithDescription("Register a repo/worktree with the running daemon so it's indexed and watched for changes. For a throwaway agent worktree, pass ephemeral=true so vor_untrack later purges its data. Returns immediately; the initial index runs in the background (poll vor_pipeline_log)."),
			mcp.WithString("path", mcp.Required(), mcp.Description("Filesystem path of the repo/worktree to track.")),
			mcp.WithBoolean("ephemeral", mcp.DefaultBool(false), mcp.Description("If true, the repo's indexed data is purged on vor_untrack (use for disposable worktrees).")),
		), s.wrap(s.toolTrack))

		s.srv.AddTool(mcp.NewTool(
			"vor_untrack",
			mcp.WithDescription("Stop watching a previously-tracked repo. An ephemeral repo's indexed data is purged; a durable repo keeps its index. Identify the repo by repository id or local path."),
			mcp.WithString("repo", mcp.Required(), mcp.Description("Repository id or local path to untrack.")),
		), s.wrap(s.toolUntrack))
	}

	s.srv.AddTool(mcp.NewTool(
		"vor_decisions",
		mcp.WithDescription("Architectural decisions extracted from the codebase. Sources include inline-marker comments (DECISION:, WHY:, TRADEOFF:), ADR files under docs/adr, BREAKING entries in CHANGELOG.md, and Conventional Commits with ! markers or BREAKING CHANGE: footers. Each record carries source provenance (file + line or commit SHA) so the agent can verify quotes."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("source", mcp.Description("Filter by source: inline_marker | adr | changelog | git_archaeology.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum records to return (1–500, default 50).")),
	), s.wrap(s.toolDecisions))

	s.srv.AddTool(mcp.NewTool(
		"vor_pages",
		mcp.WithDescription("Generated wiki page summaries (title, target file, version, freshness, token usage). Use this to discover what documentation has been produced; follow up with vor_page to read one. Pages are produced by 'vor generate' against the indexed graph."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("kind", mcp.Description("Filter by page_type: file_overview | directory_overview | symbol_detail | architecture.")),
		mcp.WithBoolean("stale_only", mcp.DefaultBool(false), mcp.Description("If true, only return pages whose freshness_status is not 'fresh' (source has drifted).")),
		mcp.WithNumber("limit", mcp.DefaultNumber(100), mcp.Description("Maximum pages to return (1–500, default 100).")),
	), s.wrap(s.toolPages))

	s.srv.AddTool(mcp.NewTool(
		"vor_page",
		mcp.WithDescription("Read one wiki page's full markdown content by target path. Returns the rendered body the LLM produced plus all the per-page metadata (model, version, freshness, source_hash) the agent needs to decide whether to trust it."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("path", mcp.Required(), mcp.Description("Repo-relative target path (e.g. 'internal/foo/bar.go').")),
		mcp.WithString("kind", mcp.DefaultString("file_overview"), mcp.Description("Page kind. Defaults to file_overview.")),
	), s.wrap(s.toolPage))
}

// wrap is the common handler wrapper: it logs each tool call (method,
// duration, success/error) so MCP usage is observable.
func (s *Server) wrap(fn func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := fn(ctx, req)
		if err != nil {
			s.opts.Logger.Error("mcp tool error", "tool", req.Params.Name, "err", err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		s.opts.Logger.Info("mcp tool", "tool", req.Params.Name)
		return res, nil
	}
}

// jsonResult is the canonical "render Go value as MCP text content" helper.
// MCP technically supports structured content but text/JSON is the lowest-
// common-denominator that every client renders well.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return mcp.NewToolResultText(string(data)), nil
}
