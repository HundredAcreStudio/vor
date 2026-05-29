package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/HundredAcreStudio/vor/internal/insights"
)

// These tools expose the shared insights read layer (the same logic behind the
// dashboard's overview panels) over MCP. Each is a thin wrapper: resolve the
// repo, call internal/insights, and return the structured result.

func (s *Server) toolGitInsights(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	gi, err := insights.GitInsightsFor(ctx, s.opts.DB, rid)
	if err != nil {
		return nil, err
	}
	return jsonResult(gi)
}

func (s *Server) toolCommitCategories(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	cc, err := insights.CommitCategories(ctx, s.opts.DB, rid)
	if err != nil {
		return nil, err
	}
	return jsonResult(cc)
}

func (s *Server) toolLanguages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	l, err := insights.Languages(ctx, s.opts.DB, rid)
	if err != nil {
		return nil, err
	}
	return jsonResult(l)
}

func (s *Server) toolModules(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	m, err := insights.Modules(ctx, s.opts.DB, rid)
	if err != nil {
		return nil, err
	}
	return jsonResult(map[string]any{"modules": m})
}

func (s *Server) toolPackages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	p, err := insights.Packages(ctx, s.opts.DB, rid)
	if err != nil {
		return nil, err
	}
	return jsonResult(p)
}

func (s *Server) toolDependencyMatrix(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dm, err := insights.DependencyMatrixFor(ctx, s.opts.DB, rid)
	if err != nil {
		return nil, err
	}
	return jsonResult(dm)
}

func (s *Server) toolEntryPoints(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	eps, err := insights.EntryPoints(ctx, s.opts.DB, rid)
	if err != nil {
		return nil, err
	}
	return jsonResult(map[string]any{"entry_points": eps})
}
