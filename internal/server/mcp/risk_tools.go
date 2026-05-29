package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/HundredAcreStudio/vor/internal/insights"
)

// toolRisk assesses the blast radius and risk of modifying one or more files,
// delegating to the shared insights layer (same logic the dashboard uses).
func (s *Server) toolRisk(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	targets := req.GetStringSlice("targets", nil)
	if len(targets) == 0 {
		if one := req.GetString("target", ""); one != "" {
			targets = []string{one}
		}
	}
	if len(targets) == 0 {
		return mcp.NewToolResultError("provide `targets` (array) or `target` (string)"), nil
	}
	out, err := insights.Risk(ctx, s.opts.DB, rid, targets)
	if err != nil {
		return nil, err
	}
	return jsonResult(map[string]any{"targets": out})
}

// toolAttention returns the prioritized "what should I look at" digest,
// delegating to the shared insights layer.
func (s *Server) toolAttention(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	items, err := insights.Attention(ctx, s.opts.DB, rid)
	if err != nil {
		return nil, err
	}
	return jsonResult(map[string]any{"items": items})
}
