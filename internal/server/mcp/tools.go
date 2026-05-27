package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerTools wires every tool onto s.srv. New tools should land here so
// the registration sits in one place and a follow-up can split this file
// per tool group when the count grows.
func (s *Server) registerTools() {
	s.srv.AddTool(mcp.NewTool(
		"repowise_status",
		mcp.WithDescription("Repository-wide summary: graph size, hotspot count, dead-code count, average health score, external dependency totals. Use this as the first tool call to understand what's been indexed."),
	), s.wrap(s.toolStatus))

	s.srv.AddTool(mcp.NewTool(
		"repowise_hotspots",
		mcp.WithDescription("Files with high git churn. Returns the top N files by churn percentile, with owner, contributor count, and bus factor. Use this to find where a refactoring effort will have outsized impact."),
		mcp.WithNumber("limit", mcp.DefaultNumber(20), mcp.Description("Maximum files to return (1–100, default 20).")),
	), s.wrap(s.toolHotspots))

	s.srv.AddTool(mcp.NewTool(
		"repowise_dead_code",
		mcp.WithDescription("Unreachable files and symbols sorted by confidence. Optional safe_only filter returns only the high-confidence (≥0.9) findings flagged safe to delete."),
		mcp.WithBoolean("safe_only", mcp.DefaultBool(false), mcp.Description("If true, only return findings flagged SafeToDelete.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum findings to return (1–500, default 50).")),
	), s.wrap(s.toolDeadCode))

	s.srv.AddTool(mcp.NewTool(
		"repowise_health",
		mcp.WithDescription("Code health summary: average score (1–10), finding counts per biomarker, and the worst-scoring files."),
		mcp.WithNumber("worst_limit", mcp.DefaultNumber(10), mcp.Description("Number of worst-scoring files to include (1–50, default 10).")),
	), s.wrap(s.toolHealth))

	s.srv.AddTool(mcp.NewTool(
		"repowise_health_findings",
		mcp.WithDescription("Paginated code-health findings, ordered by severity then impact."),
		mcp.WithString("biomarker", mcp.Description("Filter to one biomarker type (e.g. 'high_complexity').")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum findings to return (1–500, default 50).")),
	), s.wrap(s.toolHealthFindings))
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
