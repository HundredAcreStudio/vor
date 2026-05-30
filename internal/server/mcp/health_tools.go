package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/HundredAcreStudio/vor/internal/analysis/healthdiff"
)

// toolHealthDiff reports how the current per-file code health compares to the
// last committed snapshot — the regressions/improvements introduced since,
// for the "did my change help or hurt?" feedback loop.
func (s *Server) toolHealthDiff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	diff, err := healthdiff.Compute(ctx, s.opts.DB, rid)
	if err != nil {
		return nil, err
	}
	return jsonResult(diff)
}
