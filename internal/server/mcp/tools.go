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
// every tool so agents in workspace mode can address a specific repo.
// Single-repo mode treats the argument as optional (falls back to the
// server's default RepositoryID).
const repoArgDesc = "Workspace repo to query: alias from the workspace registry, or full repository id, or local filesystem path. Optional in single-repo mode (falls back to the configured default)."

// registerTools wires every tool onto s.srv. New tools should land here so
// the registration sits in one place and a follow-up can split this file
// per tool group when the count grows.
func (s *Server) registerTools() {
	// Discovery tool — only meaningful in workspace mode but cheap
	// enough to always expose.
	s.srv.AddTool(mcp.NewTool(
		"repowise_workspace_repos",
		mcp.WithDescription("List repos registered in the workspace, with aliases and full repository ids. Agents in workspace mode should call this first to discover which `repo` values are valid for other tools. Returns the empty list when the server isn't running in workspace mode."),
	), s.wrap(s.toolWorkspaceRepos))

	s.srv.AddTool(mcp.NewTool(
		"repowise_status",
		mcp.WithDescription("Repository-wide summary: graph size, hotspot count, dead-code count, average health score, external dependency totals. Use this as the first tool call to understand what's been indexed."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
	), s.wrap(s.toolStatus))

	s.srv.AddTool(mcp.NewTool(
		"repowise_hotspots",
		mcp.WithDescription("Files with high git churn. Returns the top N files by churn percentile, with owner, contributor count, and bus factor. Use this to find where a refactoring effort will have outsized impact."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithNumber("limit", mcp.DefaultNumber(20), mcp.Description("Maximum files to return (1–100, default 20).")),
	), s.wrap(s.toolHotspots))

	s.srv.AddTool(mcp.NewTool(
		"repowise_dead_code",
		mcp.WithDescription("Unreachable files and symbols sorted by confidence. Optional safe_only filter returns only the high-confidence (≥0.9) findings flagged safe to delete."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithBoolean("safe_only", mcp.DefaultBool(false), mcp.Description("If true, only return findings flagged SafeToDelete.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum findings to return (1–500, default 50).")),
	), s.wrap(s.toolDeadCode))

	s.srv.AddTool(mcp.NewTool(
		"repowise_health",
		mcp.WithDescription("Code health summary: average score (1–10), finding counts per biomarker, and the worst-scoring files."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithNumber("worst_limit", mcp.DefaultNumber(10), mcp.Description("Number of worst-scoring files to include (1–50, default 10).")),
	), s.wrap(s.toolHealth))

	s.srv.AddTool(mcp.NewTool(
		"repowise_health_findings",
		mcp.WithDescription("Paginated code-health findings, ordered by severity then impact."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("biomarker", mcp.Description("Filter to one biomarker type (e.g. 'high_complexity').")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum findings to return (1–500, default 50).")),
	), s.wrap(s.toolHealthFindings))

	s.srv.AddTool(mcp.NewTool(
		"repowise_symbol",
		mcp.WithDescription("Detail for one symbol: kind, file path, line range, visibility, complexity, PageRank. Look up by the canonical symbol_id (e.g. 'src/foo.go::User::Save')."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("symbol_id", mcp.Required(), mcp.Description("Canonical symbol ID, formatted '<file>::<symbol>' or '<file>::<parent>::<symbol>'.")),
	), s.wrap(s.toolSymbol))

	s.srv.AddTool(mcp.NewTool(
		"repowise_callers",
		mcp.WithDescription("Who calls this symbol? Returns the incoming 'calls' / 'has_method' edges from the dependency graph, with caller symbol ID and confidence."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("symbol_id", mcp.Required(), mcp.Description("Canonical symbol ID to find callers of.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum callers to return (1–500, default 50).")),
	), s.wrap(s.toolCallers))

	s.srv.AddTool(mcp.NewTool(
		"repowise_dependents",
		mcp.WithDescription("Which files import the given file? Returns incoming 'imports' edges. Use to assess the blast radius of changing a file."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("file_path", mcp.Required(), mcp.Description("Repo-relative file path (e.g. 'pkg/foo/bar.go').")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum dependents to return (1–500, default 50).")),
	), s.wrap(s.toolDependents))

	s.srv.AddTool(mcp.NewTool(
		"repowise_externals",
		mcp.WithDescription("Third-party dependencies declared in manifest files (package.json, pyproject.toml, Cargo.toml, go.mod, *.csproj). Optional ecosystem + dev filters."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("ecosystem", mcp.Description("Filter to one ecosystem: 'npm', 'pypi', 'cargo', 'go', or 'nuget'.")),
		mcp.WithBoolean("dev_only", mcp.DefaultBool(false), mcp.Description("If true, return only dev/test dependencies.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(200), mcp.Description("Maximum records to return (1–1000, default 200).")),
	), s.wrap(s.toolExternals))

	s.srv.AddTool(mcp.NewTool(
		"repowise_search",
		mcp.WithDescription("Search graph nodes (files + symbols) by name, qualified name, or node_id. Substring match, ranked by PageRank. Use when you don't already know the canonical node_id."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("query", mcp.Required(), mcp.Description("Substring to match against name / qualified_name / node_id.")),
		mcp.WithString("node_type", mcp.Description("Filter to 'file' or 'symbol'.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(25), mcp.Description("Maximum matches to return (1–200, default 25).")),
	), s.wrap(s.toolSearch))

	s.srv.AddTool(mcp.NewTool(
		"repowise_pipeline_log",
		mcp.WithDescription("Recent pipeline phase executions for this repository, newest first. Each entry is one row of pipeline_jobs (phase, state, started_at, error if failed). Useful for diagnosing 'why is my data out of date?'"),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithNumber("limit", mcp.DefaultNumber(20), mcp.Description("Maximum rows to return (1–200, default 20).")),
	), s.wrap(s.toolPipelineLog))

	s.srv.AddTool(mcp.NewTool(
		"repowise_decisions",
		mcp.WithDescription("Architectural decisions extracted from the codebase. Sources include inline-marker comments (DECISION:, WHY:, TRADEOFF:), ADR files under docs/adr, BREAKING entries in CHANGELOG.md, and Conventional Commits with ! markers or BREAKING CHANGE: footers. Each record carries source provenance (file + line or commit SHA) so the agent can verify quotes."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("source", mcp.Description("Filter by source: inline_marker | adr | changelog | git_archaeology.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum records to return (1–500, default 50).")),
	), s.wrap(s.toolDecisions))

	s.srv.AddTool(mcp.NewTool(
		"repowise_pages",
		mcp.WithDescription("Generated wiki page summaries (title, target file, version, freshness, token usage). Use this to discover what documentation has been produced; follow up with repowise_page to read one. Pages are produced by 'repowise generate' against the indexed graph."),
		mcp.WithString("repo", mcp.Description(repoArgDesc)),
		mcp.WithString("kind", mcp.Description("Filter by page_type: file_overview | directory_overview | symbol_detail | architecture.")),
		mcp.WithBoolean("stale_only", mcp.DefaultBool(false), mcp.Description("If true, only return pages whose freshness_status is not 'fresh' (source has drifted).")),
		mcp.WithNumber("limit", mcp.DefaultNumber(100), mcp.Description("Maximum pages to return (1–500, default 100).")),
	), s.wrap(s.toolPages))

	s.srv.AddTool(mcp.NewTool(
		"repowise_page",
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
