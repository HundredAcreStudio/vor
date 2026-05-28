package commands

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/logging"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/pipeline"
	"github.com/repowise-dev/repowise-go/internal/userconfig"
	"github.com/repowise-dev/repowise-go/internal/workspace"
)

// recordWatchInRegistry adds path (and optional alias / workspace
// root) to the user-global watched.json. Errors are silenced —
// telemetry is non-critical.
func recordWatchInRegistry(path, alias, workspaceRoot string) {
	reg, err := userconfig.LoadWatched()
	if err != nil {
		return
	}
	reg.RecordWatch(path, alias, workspaceRoot)
	_ = userconfig.SaveWatched(reg)
}

// recordUpdateInRegistry bumps update_count + last_updated_at for
// path. Fired once per successful pipeline.Run inside the watch loop.
func recordUpdateInRegistry(path string) {
	reg, err := userconfig.LoadWatched()
	if err != nil {
		return
	}
	reg.RecordUpdate(path)
	_ = userconfig.SaveWatched(reg)
}

// newWatchCmd starts a file-watcher that runs `repowise update` after
// any source change settles. Debounced so a burst of saves (formatter,
// IDE on-save) coalesces into one re-index. Skips .git, node_modules,
// vendor, dist, build, .repowise — the directories that churn during
// normal use without changing the indexed source.
//
// Stops cleanly on Ctrl-C (the root command's signal-aware context is
// honoured).
func newWatchCmd() *cobra.Command {
	var (
		repoPath      string
		debounceMs    int
		workspaceMode bool
	)
	cmd := &cobra.Command{
		Use:   "watch [PATH]",
		Short: "Auto-update the index on source changes (debounced)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root := repoPath
			if len(args) > 0 {
				root = args[0]
			}
			absRoot, err := filepath.Abs(root)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}
			if debounceMs <= 0 {
				debounceMs = 2000
			}
			logger := logging.New(logging.Options{
				Format: logging.FormatAuto,
				Level:  logging.ParseLevel("info"),
				Out:    os.Stderr,
			})

			if workspaceMode {
				return runWorkspaceWatch(ctx, absRoot,
					time.Duration(debounceMs)*time.Millisecond,
					logger, cmd.OutOrStdout())
			}

			conn, _, err := openDB(ctx, root)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, absRoot, "")
			if err != nil {
				return fmt.Errorf("ensure repo: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "watching %s (debounce %dms) — Ctrl-C to stop\n", absRoot, debounceMs)
			recordWatchInRegistry(absRoot, "", "")
			return runWatchLoop(ctx, watchOptions{
				Root:         absRoot,
				DB:           conn,
				RepositoryID: repoRow.ID,
				Debounce:     time.Duration(debounceMs) * time.Millisecond,
				Logger:       logger,
				ProgressOut:  out,
			})
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	cmd.Flags().IntVar(&debounceMs, "debounce", 2000, "debounce delay in ms")
	cmd.Flags().BoolVar(&workspaceMode, "workspace", false, "watch every repo registered in the workspace")
	return cmd
}

// runWorkspaceWatch spins up one fs-watcher per registered repo. Each
// watcher independently debounces and triggers `pipeline.Run` against
// its own repo on change. A single Ctrl-C cancels all of them via the
// shared context.
//
// Repos are watched in parallel because:
//   - a debounce timer on repo A shouldn't block updates on repo B
//   - fsnotify watches are non-recursive per watcher instance; one
//     watcher per repo lets each maintain its own dynamic add-set
//     for new subdirectories
func runWorkspaceWatch(
	ctx context.Context, wsRoot string, debounce time.Duration,
	logger *slog.Logger, out io.Writer,
) error {
	state, err := workspace.Load(wsRoot)
	if err != nil {
		return fmt.Errorf("load workspace.json: %w", err)
	}
	if len(state.Repos) == 0 {
		return fmt.Errorf("no repos registered at %s — `repowise workspace add PATH`", wsRoot)
	}
	fmt.Fprintf(out, "watching %d repo(s) under %s (debounce %s) — Ctrl-C to stop\n",
		len(state.Repos), wsRoot, debounce)
	for _, e := range state.Sorted() {
		fmt.Fprintf(out, "  %s → %s\n", e.Alias, e.Path)
	}

	done := make(chan error, len(state.Repos))
	for _, e := range state.Sorted() {
		entry := e
		recordWatchInRegistry(entry.Path, entry.Alias, wsRoot)
		go func() {
			conn, _, err := openDB(ctx, entry.Path)
			if err != nil {
				done <- fmt.Errorf("%s: %w", entry.Alias, err)
				return
			}
			defer conn.Close()
			repoRow, err := repos.New(conn).EnsureByLocalPath(ctx, entry.Path, entry.Alias)
			if err != nil {
				done <- fmt.Errorf("%s: %w", entry.Alias, err)
				return
			}
			err = runWatchLoop(ctx, watchOptions{
				Root:         entry.Path,
				DB:           conn,
				RepositoryID: repoRow.ID,
				Debounce:     debounce,
				Logger:       logger.With("repo", entry.Alias),
				ProgressOut:  &prefixedWriter{prefix: "[" + entry.Alias + "] ", w: out},
			})
			done <- err
		}()
	}

	// Wait for context cancel; collect any per-repo errors.
	<-ctx.Done()
	for range state.Repos {
		<-done
	}
	return nil
}

// prefixedWriter prefixes every write with a tag so concurrent
// progress lines from multiple repos remain readable.
type prefixedWriter struct {
	prefix string
	w      io.Writer
}

func (p *prefixedWriter) Write(b []byte) (int, error) {
	_, err := p.w.Write([]byte(p.prefix))
	if err != nil {
		return 0, err
	}
	return p.w.Write(b)
}

// watchOptions is the runtime contract for runWatchLoop, split out so
// tests can drive the loop without going through cobra.
type watchOptions struct {
	Root         string
	DB           *sql.DB
	RepositoryID string
	Debounce     time.Duration
	Logger       *slog.Logger
	ProgressOut  io.Writer
	// OnUpdate is invoked once after each successful update. Tests
	// signal completion through it; nil in production.
	OnUpdate func()
}

// runWatchLoop is the long-running watch + debounce + invoke loop.
func runWatchLoop(ctx context.Context, opts watchOptions) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()
	if err := addWatchDirs(w, opts.Root); err != nil {
		return fmt.Errorf("add directories: %w", err)
	}

	var (
		mu      sync.Mutex
		timer   *time.Timer
		pending int
	)

	trigger := func() {
		mu.Lock()
		hits := pending
		pending = 0
		mu.Unlock()
		opts.Logger.Info("watch: triggering update", "events", hits)
		if _, err := pipeline.Run(ctx, pipeline.Options{
			RepoPath:     opts.Root,
			RepositoryID: opts.RepositoryID,
			DB:           opts.DB,
			Mode:         pipeline.ModeUpdate,
			Logger:       opts.Logger,
		}); err != nil {
			opts.Logger.Warn("watch: update failed", "err", err)
			return
		}
		fmt.Fprintln(opts.ProgressOut, "  → updated")
		// Bump the user-global watched.json so `repowise status` can
		// surface "this repo was last reindexed at T". Failures are
		// non-critical telemetry — log + continue, don't abort the loop.
		recordUpdateInRegistry(opts.Root)
		if opts.OnUpdate != nil {
			opts.OnUpdate()
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if shouldIgnoreEvent(ev, opts.Root) {
				continue
			}
			// fsnotify reports Create on new directories — extend the
			// watch list so subdirectories don't escape.
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = w.Add(ev.Name)
				}
			}
			mu.Lock()
			pending++
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(opts.Debounce, trigger)
			mu.Unlock()
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			opts.Logger.Warn("watch: fsnotify error", "err", err)
		}
	}
}

// addWatchDirs registers every non-ignored directory under root.
func addWatchDirs(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate one-off read errors
		}
		if !d.IsDir() {
			return nil
		}
		if isIgnoredDir(filepath.Base(path)) {
			return filepath.SkipDir
		}
		_ = w.Add(path)
		return nil
	})
}

// shouldIgnoreEvent filters events for paths we deliberately don't
// want to reindex on.
func shouldIgnoreEvent(ev fsnotify.Event, root string) bool {
	rel, err := filepath.Rel(root, ev.Name)
	if err != nil {
		return true
	}
	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		if isIgnoredDir(segment) {
			return true
		}
	}
	base := filepath.Base(ev.Name)
	if strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") ||
		strings.HasSuffix(base, ".swx") {
		return true
	}
	return false
}

// isIgnoredDir lists the directory names that don't carry indexable
// source. Conservative — false negatives (watching too much) are
// cheap; false positives (watching too little) miss real edits.
func isIgnoredDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build",
		".repowise", ".idea", ".vscode", "__pycache__", ".pytest_cache",
		".next", ".turbo", "target":
		return true
	}
	return false
}
