package commands

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/logging"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/server/registry"
	"github.com/HundredAcreStudio/vor/internal/userconfig"
)

// newRegisterCmd registers a repo/worktree with the daemon so it's indexed
// and watched. When a daemon is live it's told over HTTP (so watching
// starts immediately); otherwise the repo is marked tracked in the shared
// DB and the next `vor serve` picks it up.
func newRegisterCmd() *cobra.Command {
	var ephemeral bool
	cmd := &cobra.Command{
		Use:   "register [PATH]",
		Short: "Track a repo/worktree (index + watch). Use --ephemeral for disposable worktrees.",
		Long: `Registers a repository with the serve daemon's tracked set.

If a daemon is running it's notified over its HTTP API and starts watching
immediately. If not, the repo is marked tracked in the shared database and
the next 'vor serve' picks it up.

--ephemeral marks a disposable repo (e.g. an agent's git worktree): its
indexed data is purged when you 'vor unregister' it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(argOrDot(args))
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}

			// Live daemon: register over HTTP for immediate effect.
			if base, ok := liveDaemonBaseURL(); ok {
				var repo registry.Repo
				if err := daemonPost(base, "/api/repos/register",
					map[string]any{"path": abs, "ephemeral": ephemeral}, &repo); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "registered %s (%s)%s — daemon is watching it\n",
					repo.Path, repo.ID, ephemeralSuffix(repo.Ephemeral))
				return nil
			}

			// No daemon: flip DB state directly; next serve picks it up.
			ctx := cmd.Context()
			conn, err := openDaemonDB(ctx)
			if err != nil {
				return err
			}
			defer conn.Close()
			repo, err := registry.New(conn, nil, cliLogger()).Register(ctx, abs, ephemeral)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "registered %s (%s)%s — no daemon running; it will be watched on next 'vor serve'\n",
				repo.Path, repo.ID, ephemeralSuffix(repo.Ephemeral))
			return nil
		},
	}
	cmd.Flags().BoolVar(&ephemeral, "ephemeral", false, "purge this repo's indexed data on unregister (for disposable worktrees)")
	return cmd
}

// newUnregisterCmd stops tracking a repo. Ephemeral repos are purged.
func newUnregisterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unregister [PATH|REPO_ID]",
		Short: "Stop tracking a repo (purges it if ephemeral)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Accept a repo id as-is; otherwise treat the arg as a path.
			spec := argOrDot(args)
			if abs, err := filepath.Abs(spec); err == nil && fileExists(spec) {
				spec = abs
			}

			if base, ok := liveDaemonBaseURL(); ok {
				var repo registry.Repo
				if err := daemonPost(base, "/api/repos/unregister",
					map[string]any{"repo": spec}, &repo); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "unregistered %s (%s)%s\n",
					repo.Path, repo.ID, purgedSuffix(repo.Ephemeral))
				return nil
			}

			ctx := cmd.Context()
			conn, err := openDaemonDB(ctx)
			if err != nil {
				return err
			}
			defer conn.Close()
			repo, err := registry.New(conn, nil, cliLogger()).Unregister(ctx, spec)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "unregistered %s (%s)%s\n",
				repo.Path, repo.ID, purgedSuffix(repo.Ephemeral))
			return nil
		},
	}
	return cmd
}

// ---- helpers --------------------------------------------------------

func argOrDot(args []string) string {
	if len(args) > 0 && args[0] != "" {
		return args[0]
	}
	return "."
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func ephemeralSuffix(ephemeral bool) string {
	if ephemeral {
		return " [ephemeral]"
	}
	return ""
}

func purgedSuffix(ephemeral bool) string {
	if ephemeral {
		return " — indexed data purged"
	}
	return " — index kept"
}

func cliLogger() *slog.Logger { return logging.New(logging.Options{Out: os.Stderr}) }

// openDaemonDB opens (and migrates) the shared global database: VOR_DB_URL
// when set, otherwise ~/.config/vor/vor.db. Used by the CLI when no daemon is
// running.
func openDaemonDB(ctx context.Context) (*sql.DB, error) {
	boot := config.LoadBootstrap()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: boot.DatabaseURL})
	if err != nil {
		return nil, err
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		conn.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return conn, nil
}

// liveDaemonBaseURL returns the base URL of a running daemon, or ok=false.
func liveDaemonBaseURL() (string, bool) {
	info, err := userconfig.LoadDaemon()
	if err != nil || info == nil || info.Addr == "" || !info.Alive() {
		return "", false
	}
	host, port, err := net.SplitHostPort(info.Addr)
	if err != nil {
		return "", false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port), true
}

// daemonPost POSTs body as JSON to base+path and decodes a 200 response
// into out. A non-200 is turned into an error carrying the server message.
func daemonPost(base, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(base+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("daemon: %s", e.Error)
		}
		return fmt.Errorf("daemon returned %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
