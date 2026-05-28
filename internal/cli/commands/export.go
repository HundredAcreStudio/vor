package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

// newExportCmd dumps wiki_pages (and optionally decisions / hotspots /
// dead-code for --full --format=json) to disk. Mirrors the Python
// command's three formats: markdown (one .md per page), json (single
// file), html (one .html per page; wrapped in basic <pre> tags — no
// markdown-to-HTML conversion in this port).
func newExportCmd() *cobra.Command {
	o := &exportOptions{}
	cmd := &cobra.Command{
		Use:   "export [PATH]",
		Short: "Export persisted wiki pages to disk",
		Long: `Writes one file per page (markdown / html) or a single JSON dump
to <repo>/.repowise/export by default. Use --full --format=json to
include the cross-cutting analysis tables (decisions, dead code,
hotspots) in the JSON dump.

The HTML output is a bare wrapper around the raw markdown — no
markdown-to-HTML conversion in this implementation to avoid pulling
in a markdown library dependency.`,
		Args: cobra.MaximumNArgs(1),
		RunE: o.run,
	}
	cmd.Flags().StringVar(&o.repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	cmd.Flags().StringVar(&o.format, "format", "markdown", "output format: markdown | html | json")
	cmd.Flags().StringVarP(&o.outputDir, "output", "o", "", "output directory (default <repo>/.repowise/export)")
	cmd.Flags().BoolVar(&o.full, "full", false, "include decisions/hotspots/dead-code in JSON export")
	return cmd
}

// exportOptions holds the export command's flags.
type exportOptions struct {
	repoPath, format, outputDir string
	full                        bool
}

func (o *exportOptions) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	root := o.repoPath
	if len(args) > 0 {
		root = args[0]
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	if o.format != "markdown" && o.format != "html" && o.format != "json" {
		return fmt.Errorf("--format must be markdown | html | json (got %q)", o.format)
	}

	conn, _, err := openDB(ctx, root)
	if err != nil {
		return err
	}
	defer conn.Close()
	repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, absRoot, "")
	if err != nil {
		return fmt.Errorf("locate repo: %w", err)
	}

	outDir, err := resolveExportDir(absRoot, o.outputDir)
	if err != nil {
		return err
	}

	pages, err := loadExportPages(ctx, conn, repoRow.ID)
	if err != nil {
		return fmt.Errorf("load pages: %w", err)
	}
	if len(pages) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no pages found. Run `repowise generate` first.")
		return nil
	}

	if err := o.writeExport(ctx, conn, repoRow.ID, outDir, pages); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "exported %d pages to %s\n", len(pages), outDir)
	return nil
}

// resolveExportDir picks the output directory (default <repo>/.repowise/
// export) and ensures it exists.
func resolveExportDir(absRoot, outputDir string) (string, error) {
	outDir := outputDir
	if outDir == "" {
		outDir = filepath.Join(absRoot, ".repowise", "export")
	} else {
		abs, err := filepath.Abs(outDir)
		if err != nil {
			return "", fmt.Errorf("resolve --output: %w", err)
		}
		outDir = abs
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	return outDir, nil
}

// writeExport dispatches to the per-format writer.
func (o *exportOptions) writeExport(ctx context.Context, conn *sql.DB, repoID, outDir string, pages []exportPage) error {
	switch o.format {
	case "markdown":
		return writeMarkdownExport(outDir, pages)
	case "html":
		return writeHTMLExport(outDir, pages)
	case "json":
		extras := map[string]any{}
		if o.full {
			ex, err := loadExportExtras(ctx, conn, repoID)
			if err != nil {
				return fmt.Errorf("load extras: %w", err)
			}
			extras = ex
		}
		return writeJSONExport(outDir, pages, extras, o.full)
	}
	return nil
}

type exportPage struct {
	ID           string
	PageType     string
	Title        string
	Content      string
	TargetPath   string
	Confidence   float64
	Freshness    string
	Version      int
	ModelName    string
	ProviderName string
}

func loadExportPages(ctx context.Context, conn *sql.DB, repoID string) ([]exportPage, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT id, page_type, title, content, target_path,
		       confidence, freshness_status, version, model_name, provider_name
		FROM wiki_pages
		WHERE repository_id = ?
		ORDER BY target_path
	`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []exportPage{}
	for rows.Next() {
		var p exportPage
		if err := rows.Scan(&p.ID, &p.PageType, &p.Title, &p.Content, &p.TargetPath,
			&p.Confidence, &p.Freshness, &p.Version, &p.ModelName, &p.ProviderName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func loadExportExtras(ctx context.Context, conn *sql.DB, repoID string) (map[string]any, error) {
	extras := map[string]any{}

	// Decisions
	rows, err := conn.QueryContext(ctx, `
		SELECT title, status, decision, rationale, confidence, source,
		       affected_files_json, COALESCE(staleness_score, 0)
		FROM decision_records WHERE repository_id = ?
	`, repoID)
	if err != nil {
		return nil, err
	}
	decisions := []map[string]any{}
	for rows.Next() {
		var title, status, decision, rationale, source, affectedJSON string
		var conf, stale float64
		if err := rows.Scan(&title, &status, &decision, &rationale, &conf, &source, &affectedJSON, &stale); err != nil {
			rows.Close()
			return nil, err
		}
		var affected []string
		_ = json.Unmarshal([]byte(affectedJSON), &affected)
		decisions = append(decisions, map[string]any{
			"title":           title,
			"status":          status,
			"decision":        decision,
			"rationale":       rationale,
			"confidence":      conf,
			"source":          source,
			"affected_files":  affected,
			"staleness_score": stale,
		})
	}
	rows.Close()
	extras["decisions"] = decisions

	// Dead code
	rows, err = conn.QueryContext(ctx, `
		SELECT file_path, COALESCE(symbol_name, ''), kind, confidence, safe_to_delete
		FROM dead_code_findings WHERE repository_id = ?
	`, repoID)
	if err != nil {
		return nil, err
	}
	dead := []map[string]any{}
	for rows.Next() {
		var path, symbol, kind string
		var conf float64
		var safe bool
		if err := rows.Scan(&path, &symbol, &kind, &conf, &safe); err != nil {
			rows.Close()
			return nil, err
		}
		dead = append(dead, map[string]any{
			"file_path":      path,
			"symbol_name":    symbol,
			"finding_type":   kind,
			"confidence":     conf,
			"safe_to_delete": safe,
		})
	}
	rows.Close()
	extras["dead_code"] = dead

	// Hotspots
	rows, err = conn.QueryContext(ctx, `
		SELECT file_path, churn_percentile, commit_count_90d,
		       COALESCE(primary_owner_name, ''), bus_factor
		FROM git_metadata WHERE repository_id = ? AND is_hotspot = 1
		ORDER BY churn_percentile DESC LIMIT 50
	`, repoID)
	if err != nil {
		return nil, err
	}
	hotspots := []map[string]any{}
	for rows.Next() {
		var path, owner string
		var pct float64
		var commits, bf int
		if err := rows.Scan(&path, &pct, &commits, &owner, &bf); err != nil {
			rows.Close()
			return nil, err
		}
		hotspots = append(hotspots, map[string]any{
			"file_path":        path,
			"churn_percentile": pct,
			"commit_count_90d": commits,
			"primary_owner":    owner,
			"bus_factor":       bf,
		})
	}
	rows.Close()
	extras["hotspots"] = hotspots

	return extras, nil
}

func writeMarkdownExport(outDir string, pages []exportPage) error {
	for _, p := range pages {
		name := safeExportFilename(p.TargetPath) + ".md"
		body := "# " + p.Title + "\n\n" + p.Content
		if err := os.WriteFile(filepath.Join(outDir, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeHTMLExport(outDir string, pages []exportPage) error {
	// Plain <pre> fallback — see command Long doc for the rationale.
	for _, p := range pages {
		name := safeExportFilename(p.TargetPath) + ".html"
		html := "<!DOCTYPE html>\n<html>\n<head>\n" +
			"<title>" + escapeHTML(p.Title) + "</title>\n" +
			"<meta charset=\"utf-8\">\n" +
			"</head>\n<body>\n" +
			"<h1>" + escapeHTML(p.Title) + "</h1>\n" +
			"<pre>" + escapeHTML(p.Content) + "</pre>\n" +
			"</body>\n</html>\n"
		if err := os.WriteFile(filepath.Join(outDir, name), []byte(html), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeJSONExport(outDir string, pages []exportPage, extras map[string]any, full bool) error {
	entries := make([]map[string]any, 0, len(pages))
	for _, p := range pages {
		entry := map[string]any{
			"page_id":     p.ID,
			"page_type":   p.PageType,
			"title":       p.Title,
			"content":     p.Content,
			"target_path": p.TargetPath,
		}
		if full {
			entry["confidence"] = p.Confidence
			entry["freshness_status"] = p.Freshness
			entry["version"] = p.Version
			entry["model_name"] = p.ModelName
			entry["provider_name"] = p.ProviderName
		}
		entries = append(entries, entry)
	}
	envelope := map[string]any{"pages": entries}
	if full {
		for k, v := range extras {
			envelope[k] = v
		}
	}
	body, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "wiki_pages.json"), body, 0o644)
}

// safeExportFilename mirrors the Python implementation's per-char
// replacements so the same source paths produce the same export
// filenames across implementations.
func safeExportFilename(p string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"::", "__",
		"->", "--",
		">", "",
		"<", "",
		":", "_",
		"\\", "_",
		"|", "_",
		"?", "",
		"*", "",
		"\"", "",
	)
	return replacer.Replace(p)
}

// escapeHTML is a minimal escaper for the HTML fallback path.
func escapeHTML(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
	).Replace(s)
}
