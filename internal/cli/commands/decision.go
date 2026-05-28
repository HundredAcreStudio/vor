package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/analysis/decisions"
	"github.com/repowise-dev/repowise-go/internal/persistence/decisionstore"
)

// newDecisionCmd is the decision subcommand group. The existing
// `decisions` (plural, read-only) command is preserved as an alias for
// `decision list` to keep older muscle memory + scripts working.
func newDecisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "decision",
		Aliases: []string{"decisions"},
		Short:   "Inspect, mutate, and triage architectural decisions",
	}
	cmd.AddCommand(newDecisionListCmd())
	cmd.AddCommand(newDecisionShowCmd())
	cmd.AddCommand(newDecisionAddCmd())
	cmd.AddCommand(newDecisionConfirmCmd())
	cmd.AddCommand(newDecisionDismissCmd())
	cmd.AddCommand(newDecisionDeprecateCmd())
	cmd.AddCommand(newDecisionHealthCmd())
	return cmd
}

// newDecisionListCmd is the read-only listing command. Mirrors the
// existing `decisions` command's behaviour so users get the same
// tabular output by either name.
func newDecisionListCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
		source   string
		status   string
		limit    int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List decisions for a repository",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
			}
			query := `SELECT id, title, status, source, COALESCE(evidence_file,''),
			                COALESCE(evidence_line,0), confidence, verification
			          FROM decision_records WHERE repository_id = ?`
			qargs := []any{repoRow.ID}
			if source != "" {
				query += " AND source = ?"
				qargs = append(qargs, source)
			}
			if status != "" {
				query += " AND status = ?"
				qargs = append(qargs, status)
			}
			query += " ORDER BY confidence DESC, evidence_file, evidence_line LIMIT ?"
			qargs = append(qargs, limit)

			rows, err := conn.QueryContext(ctx, query, qargs...)
			if err != nil {
				return err
			}
			defer rows.Close()
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  id\tconf\tsource\tstatus\tlocation\ttitle")
			count := 0
			for rows.Next() {
				var id, title, st, src, file, ver string
				var line int
				var conf float64
				if err := rows.Scan(&id, &title, &st, &src, &file, &line, &conf, &ver); err != nil {
					return err
				}
				loc := file
				if line > 0 {
					loc = fmt.Sprintf("%s:%d", file, line)
				}
				short := id
				if len(short) > 8 {
					short = short[:8]
				}
				fmt.Fprintf(tw, "  %s\t%.2f\t%s\t%s\t%s\t%s\n", short, conf, src, st, loc, title)
				count++
			}
			if count == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no decisions found (run `repowise init` first)")
			}
			return rows.Err()
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().StringVar(&source, "source", "", "filter by source (inline_marker|adr|changelog|git_archaeology|cli)")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (active|proposed|deprecated|superseded)")
	cmd.Flags().IntVar(&limit, "limit", 50, "max records to show")
	return cmd
}

func newDecisionShowCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
	)
	cmd := &cobra.Command{
		Use:   "show ID",
		Short: "Print one decision's full record by id (8-char prefix accepted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
			}
			id, err := resolveDecisionID(ctx, conn, repoRow.ID, args[0])
			if err != nil {
				return err
			}
			rec, _, err := decisionstore.New(conn).Get(ctx, id)
			if err != nil {
				return fmt.Errorf("fetch decision: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "id:           %s\n", id)
			fmt.Fprintf(out, "title:        %s\n", rec.Title)
			fmt.Fprintf(out, "status:       %s\n", rec.Status)
			fmt.Fprintf(out, "source:       %s\n", rec.Source)
			fmt.Fprintf(out, "confidence:   %.2f (%s)\n", rec.Confidence, rec.Verification)
			if rec.EvidenceFile != "" {
				fmt.Fprintf(out, "evidence:     %s:%d\n", rec.EvidenceFile, rec.EvidenceLine)
			}
			if len(rec.Tags) > 0 {
				fmt.Fprintf(out, "tags:         %s\n", strings.Join(rec.Tags, ", "))
			}
			if len(rec.AffectedFiles) > 0 {
				fmt.Fprintf(out, "files:        %s\n", strings.Join(rec.AffectedFiles, ", "))
			}
			if rec.Context != "" {
				fmt.Fprintf(out, "\n## Context\n%s\n", rec.Context)
			}
			if rec.Decision != "" {
				fmt.Fprintf(out, "\n## Decision\n%s\n", rec.Decision)
			}
			if rec.Rationale != "" {
				fmt.Fprintf(out, "\n## Rationale\n%s\n", rec.Rationale)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	return cmd
}

func newDecisionAddCmd() *cobra.Command {
	var (
		repoPath     string
		repoID       string
		title        string
		decisionText string
		rationale    string
		status       string
		tagsCSV      string
		affectedCSV  string
		confidence   float64
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new decision record by hand (source=cli)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				return errors.New("--title is required")
			}
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
			}
			rec := decisions.Record{
				Title:         title,
				Decision:      decisionText,
				Rationale:     rationale,
				Status:        status,
				Source:        "cli",
				Confidence:    confidence,
				Verification:  decisions.VerificationExact,
				Tags:          splitCSV(tagsCSV),
				AffectedFiles: splitCSV(affectedCSV),
			}
			id, err := decisionstore.New(conn).Insert(ctx, repoRow.ID, rec)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added decision %s (%s)\n", id[:8], title)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().StringVar(&title, "title", "", "decision title (required)")
	cmd.Flags().StringVar(&decisionText, "decision", "", "what was decided")
	cmd.Flags().StringVar(&rationale, "rationale", "", "why this decision was made")
	cmd.Flags().StringVar(&status, "status", "active", "active|proposed|deprecated|superseded")
	cmd.Flags().StringVar(&tagsCSV, "tags", "", "comma-separated tags")
	cmd.Flags().StringVar(&affectedCSV, "affected-files", "", "comma-separated repo-relative file paths")
	cmd.Flags().Float64Var(&confidence, "confidence", 1.0, "0–1 confidence")
	return cmd
}

func newDecisionConfirmCmd() *cobra.Command {
	return newDecisionStatusCmd("confirm",
		"Mark a decision as active with full confidence (exact verification)",
		"active", 1.0, decisions.VerificationExact, true)
}

func newDecisionDismissCmd() *cobra.Command {
	return newDecisionStatusCmd("dismiss",
		"Reject a decision (status=deprecated, confidence=0)",
		"deprecated", 0.0, "", true)
}

func newDecisionDeprecateCmd() *cobra.Command {
	return newDecisionStatusCmd("deprecate",
		"Mark a decision as deprecated (still recorded, no longer in force)",
		"deprecated", -1, "", false)
}

// newDecisionStatusCmd is the shared scaffold for the three mutation
// subcommands. flipConfidence toggles whether confidence/verification
// are updated alongside the status flip.
func newDecisionStatusCmd(name, short, newStatus string, newConfidence float64, newVerification string, flipConfidence bool) *cobra.Command {
	var (
		repoPath string
		repoID   string
	)
	cmd := &cobra.Command{
		Use:   name + " ID",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
			}
			id, err := resolveDecisionID(ctx, conn, repoRow.ID, args[0])
			if err != nil {
				return err
			}
			store := decisionstore.New(conn)
			if err := store.SetStatus(ctx, id, newStatus); err != nil {
				return err
			}
			if flipConfidence {
				if err := store.SetConfidence(ctx, id, newConfidence, newVerification); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "decision %s → status=%s\n", id[:8], newStatus)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	return cmd
}

func newDecisionHealthCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
	)
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Summarise the decision corpus (counts by source / status, low-confidence list)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			var total int
			_ = conn.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM decision_records WHERE repository_id = ?`, repoRow.ID).Scan(&total)
			fmt.Fprintf(out, "decisions:  %d total\n\n", total)
			if total == 0 {
				return nil
			}

			// By source.
			rows, _ := conn.QueryContext(ctx, `
				SELECT source, COUNT(*) FROM decision_records
				WHERE repository_id = ? GROUP BY source ORDER BY 2 DESC
			`, repoRow.ID)
			fmt.Fprintln(out, "by source:")
			for rows.Next() {
				var src string
				var n int
				_ = rows.Scan(&src, &n)
				fmt.Fprintf(out, "  %-20s %d\n", src, n)
			}
			rows.Close()

			// By status.
			rows, _ = conn.QueryContext(ctx, `
				SELECT status, COUNT(*) FROM decision_records
				WHERE repository_id = ? GROUP BY status ORDER BY 2 DESC
			`, repoRow.ID)
			fmt.Fprintln(out, "\nby status:")
			for rows.Next() {
				var st string
				var n int
				_ = rows.Scan(&st, &n)
				fmt.Fprintf(out, "  %-20s %d\n", st, n)
			}
			rows.Close()

			// Low-confidence (under 0.5).
			rows, _ = conn.QueryContext(ctx, `
				SELECT id, title, confidence FROM decision_records
				WHERE repository_id = ? AND confidence < 0.5
				ORDER BY confidence ASC LIMIT 10
			`, repoRow.ID)
			low := []string{}
			for rows.Next() {
				var id, title string
				var conf float64
				_ = rows.Scan(&id, &title, &conf)
				low = append(low, fmt.Sprintf("  %s  %.2f  %s", id[:8], conf, title))
			}
			rows.Close()
			if len(low) > 0 {
				fmt.Fprintln(out, "\nlow confidence (review candidates):")
				for _, l := range low {
					fmt.Fprintln(out, l)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	return cmd
}

// resolveDecisionID accepts either a full id or a unique prefix
// (typically the 8-char display short-id) and returns the canonical
// full id. Ambiguous prefixes error.
func resolveDecisionID(ctx context.Context, conn *sql.DB, repoID, input string) (string, error) {
	if input == "" {
		return "", errors.New("decision id required")
	}
	if len(input) >= 32 {
		// Treat as full id; verify it exists for this repo.
		var ok string
		err := conn.QueryRowContext(ctx,
			`SELECT id FROM decision_records WHERE id = ? AND repository_id = ?`, input, repoID).Scan(&ok)
		if err != nil {
			return "", fmt.Errorf("no decision found with id %s", input)
		}
		return ok, nil
	}
	rows, err := conn.QueryContext(ctx,
		`SELECT id FROM decision_records WHERE repository_id = ? AND id LIKE ? LIMIT 2`,
		repoID, input+"%")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	matches := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		matches = append(matches, id)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no decision found with id prefix %s", input)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous id prefix %s — narrow it down", input)
	}
}

// splitCSV splits a comma-separated string into a trimmed []string,
// dropping empty entries. Used by --tags and --affected-files.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}
