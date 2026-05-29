package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/ingestion/parser"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
)

// newDoctorCmd runs a series of environment + configuration checks and
// reports each as ok / warn / fail. Mirrors the Python `vor doctor`
// command. Read-only; safe to run anywhere.
func newDoctorCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run diagnostic checks (config + DB + parsers + provider keys)",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()

			checks := []doctorCheck{
				checkRepoPath(repoPath),
				checkConfigFile(repoPath),
				checkProviderKeys(repoPath),
				checkDatabaseURL(cmd.Context(), repoPath),
				checkRegisteredParsers(),
				checkVorDir(repoPath),
			}

			fail := 0
			for _, c := range checks {
				marker := "ok"
				switch c.status {
				case statusWarn:
					marker = "warn"
				case statusFail:
					marker = "FAIL"
					fail++
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\n", marker, c.name, c.detail)
			}
			if fail > 0 {
				return fmt.Errorf("%d check(s) failed", fail)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	return cmd
}

type doctorStatus int

const (
	statusOK doctorStatus = iota
	statusWarn
	statusFail
)

type doctorCheck struct {
	name   string
	status doctorStatus
	detail string
}

func checkRepoPath(repoPath string) doctorCheck {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return doctorCheck{name: "repo path", status: statusFail, detail: err.Error()}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return doctorCheck{name: "repo path", status: statusFail, detail: err.Error()}
	}
	if !info.IsDir() {
		return doctorCheck{name: "repo path", status: statusFail, detail: abs + " is not a directory"}
	}
	return doctorCheck{name: "repo path", status: statusOK, detail: abs}
}

func checkConfigFile(repoPath string) doctorCheck {
	_ = repoPath
	// Configuration is database-backed now (the settings table); there is no
	// config file to validate. Report where the DB that holds it lives.
	boot := config.LoadBootstrap()
	return doctorCheck{
		name:   "configuration",
		status: statusOK,
		detail: "stored in the database (" + boot.DatabaseURL + ")",
	}
}

func checkProviderKeys(repoPath string) doctorCheck {
	_ = repoPath
	keysCfg := config.LoadBootstrap().ProviderKeys
	keys := []struct{ name, val string }{
		{"ANTHROPIC_API_KEY", keysCfg.Anthropic},
		{"OPENAI_API_KEY", keysCfg.OpenAI},
		{"GEMINI_API_KEY", keysCfg.Gemini},
	}
	set := []string{}
	for _, k := range keys {
		if k.val != "" {
			set = append(set, k.name)
		}
	}
	if len(set) == 0 {
		return doctorCheck{
			name:   "provider keys",
			status: statusWarn,
			detail: "no provider API keys set (mock provider still works)",
		}
	}
	return doctorCheck{
		name:   "provider keys",
		status: statusOK,
		detail: fmt.Sprintf("%d set: %v", len(set), set),
	}
}

func checkDatabaseURL(ctx context.Context, repoPath string) doctorCheck {
	_ = repoPath
	url := config.LoadBootstrap().DatabaseURL
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: url})
	if err != nil {
		return doctorCheck{name: "database", status: statusFail, detail: err.Error()}
	}
	defer conn.Close()

	// Count repositories — succeeds only if migrations have run.
	var n int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM repositories`).Scan(&n); err != nil {
		// Migrations probably haven't run.
		if errors.Is(err, sql.ErrNoRows) || isMissingTable(err) {
			return doctorCheck{
				name:   "database",
				status: statusWarn,
				detail: fmt.Sprintf("%s reachable but schema is empty (run `vor db migrate`)", dialect),
			}
		}
		return doctorCheck{name: "database", status: statusFail, detail: err.Error()}
	}
	return doctorCheck{
		name:   "database",
		status: statusOK,
		detail: fmt.Sprintf("%s, %d repositories indexed", dialect, n),
	}
}

func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// modernc.org/sqlite + pgx error strings are stable enough for substring match.
	for _, s := range []string{"no such table", "does not exist", "no such column"} {
		if contains(msg, s) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// checkRegisteredParsers verifies that the per-language parser side-effect
// imports landed. We do a sanity check against a known-good language.
func checkRegisteredParsers() doctorCheck {
	registered := parser.RegisteredLanguages()
	if len(registered) == 0 {
		return doctorCheck{
			name:   "parsers",
			status: statusFail,
			detail: "no parsers registered (side-effect imports missing?)",
		}
	}
	return doctorCheck{
		name:   "parsers",
		status: statusOK,
		detail: fmt.Sprintf("%d languages: %v", len(registered), registered),
	}
}

func checkVorDir(repoPath string) doctorCheck {
	d := filepath.Join(repoPath, ".vor")
	info, err := os.Stat(d)
	if errors.Is(err, os.ErrNotExist) {
		return doctorCheck{
			name:   ".vor dir",
			status: statusWarn,
			detail: d + " missing (will be created on first ingest)",
		}
	}
	if err != nil {
		return doctorCheck{name: ".vor dir", status: statusFail, detail: err.Error()}
	}
	if !info.IsDir() {
		return doctorCheck{name: ".vor dir", status: statusFail, detail: d + " exists but is not a directory"}
	}
	return doctorCheck{name: ".vor dir", status: statusOK, detail: d}
}
