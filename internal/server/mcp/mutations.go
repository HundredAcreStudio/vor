package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/HundredAcreStudio/vor/internal/analysis/security"
	"github.com/HundredAcreStudio/vor/internal/ingestion/traverser"
	"github.com/HundredAcreStudio/vor/internal/persistence/pipelinestore"
	"github.com/HundredAcreStudio/vor/internal/persistence/securitystore"
	"github.com/HundredAcreStudio/vor/internal/pipeline"
)

// ---- tool: vor_reindex ----------------------------------------------
//
// Mutating tool: kicks off a pipeline run for a repo so the index reflects
// the current source. Asynchronous — re-indexing can take seconds to
// minutes and would blow the request/response budget, so this returns a
// run_id immediately and the caller polls vor_pipeline_log for phase
// progress. Mode "update" (default) is incremental (reuses the parse
// cache); "init" forces a fresh pass.

func (s *Server) toolReindex(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repoPath, err := s.repoPathForID(ctx, rid)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	mode := pipeline.ModeUpdate
	if req.GetString("mode", "update") == "init" {
		mode = pipeline.ModeInit
	}

	pstore := pipelinestore.New(s.opts.DB)
	// Guard: don't double-fire. If the latest run is still going, return it.
	if latest, err := pstore.LatestRun(ctx, rid); err == nil && latest != nil &&
		latest.Overall == pipelinestore.OutcomeRunning {
		return jsonResult(map[string]any{
			"status": "already_running",
			"runId":  latest.RunID,
			"repo":   rid,
			"note":   "a run is already in progress; poll vor_pipeline_log",
		})
	}

	runID := uuid.NewString()
	logger := s.logger()

	// Run in the background with a detached context so the work survives the
	// tool call returning. The daemon's lifetime owns it.
	go func() {
		bg := context.Background()
		if _, err := pipeline.Run(bg, pipeline.Options{
			RepoPath:     repoPath,
			Mode:         mode,
			DB:           s.opts.DB,
			RepositoryID: rid,
			RunID:        runID,
			Logger:       logger,
		}); err != nil {
			logger.Error("reindex run failed", "repo", rid, "runId", runID, "err", err)
		}
	}()

	return jsonResult(map[string]any{
		"status": "started",
		"runId":  runID,
		"repo":   rid,
		"mode":   string(mode),
		"note":   "poll vor_pipeline_log for phase progress",
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

// logger returns the configured logger or the default.
func (s *Server) logger() *slog.Logger {
	if s.opts.Logger != nil {
		return s.opts.Logger
	}
	return slog.Default()
}
