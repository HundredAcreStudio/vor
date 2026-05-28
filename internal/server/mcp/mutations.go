package mcp

import (
	"context"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/HundredAcreStudio/vor/internal/analysis/security"
	"github.com/HundredAcreStudio/vor/internal/ingestion/traverser"
	"github.com/HundredAcreStudio/vor/internal/persistence/securitystore"
)

// ---- tools: vor_track / vor_untrack ---------------------------------
//
// Register/unregister a repo with the running daemon. An agent working in
// a throwaway git worktree calls vor_track with ephemeral=true on start and
// vor_untrack on teardown — the daemon indexes + watches it while it lives
// and purges its data when it's gone.

func (s *Server) toolTrack(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.opts.Registrar == nil {
		return mcp.NewToolResultError("registration unavailable (no watcher running)"), nil
	}
	path := req.GetString("path", "")
	if path == "" {
		return mcp.NewToolResultError("`path` is required"), nil
	}
	repo, err := s.opts.Registrar.Register(ctx, path, req.GetBool("ephemeral", false))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{
		"status":    "tracking",
		"repo":      repo.ID,
		"path":      repo.Path,
		"ephemeral": repo.Ephemeral,
		"note":      "initial index started; the daemon now watches this path",
	})
}

func (s *Server) toolUntrack(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.opts.Registrar == nil {
		return mcp.NewToolResultError("registration unavailable (no watcher running)"), nil
	}
	spec := req.GetString("repo", "")
	if spec == "" {
		return mcp.NewToolResultError("`repo` (id or path) is required"), nil
	}
	repo, err := s.opts.Registrar.Unregister(ctx, spec)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	status := "untracked"
	if repo.Ephemeral {
		status = "untracked_and_purged"
	}
	return jsonResult(map[string]any{
		"status":    status,
		"repo":      repo.ID,
		"path":      repo.Path,
		"ephemeral": repo.Ephemeral,
	})
}

// ---- tool: vor_security_scan ----------------------------------------
//
// Mutating tool: runs the pattern-based security scanner over the repo and
// replaces the stored findings. Synchronous — the scan is regex over source
// (no cgo, no LLM), fast enough to return results inline. Read them back
// with vor_security.

func (s *Server) toolSecurityScan(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repoPath, err := s.repoPathForID(ctx, rid)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	tr, err := traverser.New(traverser.Options{RepoRoot: repoPath})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	files, _, err := tr.Collect(ctx)
	if err != nil && err != ctx.Err() {
		return nil, fmt.Errorf("walk: %w", err)
	}

	var findings []security.Finding
	for _, f := range files {
		data, err := os.ReadFile(f.AbsPath)
		if err != nil {
			continue
		}
		findings = append(findings, security.Scan(f.Path, data)...)
	}
	if err := securitystore.New(s.opts.DB).ReplaceAll(ctx, rid, findings); err != nil {
		return nil, fmt.Errorf("store findings: %w", err)
	}

	bySeverity := map[string]int{}
	for _, f := range findings {
		bySeverity[f.Severity]++
	}
	return jsonResult(map[string]any{
		"repo":         rid,
		"scannedFiles": len(files),
		"findings":     len(findings),
		"bySeverity":   bySeverity,
		"note":         "read findings with vor_security",
	})
}

// repoPathForID returns the repository's local filesystem path. Mutating
// tools need it to drive the traverser / pipeline.
func (s *Server) repoPathForID(ctx context.Context, rid string) (string, error) {
	var path string
	err := s.opts.DB.QueryRowContext(ctx,
		`SELECT local_path FROM repositories WHERE id = ?`, rid).Scan(&path)
	if err != nil {
		return "", fmt.Errorf("repo %q: local path not found: %w", rid, err)
	}
	if path == "" {
		return "", fmt.Errorf("repo %q has no local path on record", rid)
	}
	return path, nil
}
