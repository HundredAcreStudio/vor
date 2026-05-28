// Package autoindex keeps a serving daemon's index fresh without manual
// `vor_reindex` calls. On start it runs one incremental reindex per
// indexed repo (so a daemon launched before its first ingest finished
// doesn't serve an empty graph), then watches each repo's source tree and
// re-runs an incremental pipeline on change.
//
// The server layer holds no in-memory graph — every MCP/HTTP handler
// queries the DB live — so a completed reindex is visible to the next
// request with no hot-swap. That makes auto-reindex a matter of deciding
// *when* to run the pipeline, which is all this package does.
package autoindex

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"

	"github.com/HundredAcreStudio/vor/internal/ingestion/traverser"
	"github.com/HundredAcreStudio/vor/internal/persistence/pipelinestore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/pipeline"
)

// defaultDebounce collapses a burst of filesystem events (a git checkout,
// a formatter rewriting a tree, an editor's atomic-save dance) into one
// reindex once the dust settles.
const defaultDebounce = 1500 * time.Millisecond

// Options configures a Watcher.
type Options struct {
	// DB is the open *sql.DB shared with the serving handlers. Required.
	DB *sql.DB
	// Logger receives lifecycle + per-reindex events. Defaults to
	// slog.Default() when nil.
	Logger *slog.Logger
	// Debounce is the quiet period after the last filesystem event before a
	// reindex fires. Defaults to defaultDebounce when zero.
	Debounce time.Duration
}

// Watcher runs startup reindexes and per-repo filesystem watching.
type Watcher struct {
	db       *sql.DB
	logger   *slog.Logger
	debounce time.Duration
}

// New builds a Watcher from opts.
func New(opts Options) *Watcher {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	debounce := opts.Debounce
	if debounce <= 0 {
		debounce = defaultDebounce
	}
	return &Watcher{db: opts.DB, logger: logger, debounce: debounce}
}

// Run reindexes every locally-present repo once, then watches each for
// changes until ctx is cancelled. It blocks for the daemon's lifetime.
// An in-flight reindex shares ctx, so shutdown cancels it cleanly.
func (w *Watcher) Run(ctx context.Context) error {
	all, err := repos.New(w.db).List(ctx)
	if err != nil {
		return err
	}

	var targets []*repoWatcher
	for _, r := range all {
		if r.LocalPath == "" {
			continue
		}
		if fi, err := os.Stat(r.LocalPath); err != nil || !fi.IsDir() {
			w.logger.Warn("auto-reindex: skipping repo whose local path is gone",
				"repo", r.ID, "path", r.LocalPath)
			continue
		}
		targets = append(targets, &repoWatcher{
			db:       w.db,
			logger:   w.logger,
			debounce: w.debounce,
			repoID:   r.ID,
			root:     r.LocalPath,
		})
	}
	if len(targets) == 0 {
		w.logger.Info("auto-reindex: no local repos to watch")
		<-ctx.Done()
		return nil
	}

	// Startup pass: incremental reindex of each repo so first queries are
	// fresh. Sequential — these share the daemon's CPU with request serving
	// and there's no value in stampeding them.
	for _, rw := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rw.reindex(ctx, "startup")
	}

	w.logger.Info("auto-reindex: watching for changes", "repos", len(targets))
	var wg sync.WaitGroup
	for _, rw := range targets {
		wg.Add(1)
		go func(rw *repoWatcher) {
			defer wg.Done()
			rw.watch(ctx)
		}(rw)
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

// repoWatcher owns the fsnotify watch + debounce loop for one repository.
type repoWatcher struct {
	db       *sql.DB
	logger   *slog.Logger
	debounce time.Duration
	repoID   string
	root     string
}

// watch installs filesystem watches on the repo's source directories and
// fires a debounced incremental reindex on any change, re-deriving the
// watch set after each run so newly-created directories get covered.
func (rw *repoWatcher) watch(ctx context.Context) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		rw.logger.Error("auto-reindex: cannot create fs watcher", "repo", rw.repoID, "err", err)
		return
	}
	defer fw.Close()

	watched := map[string]bool{}
	rw.syncWatches(fw, watched)

	// A stopped timer we reset on each event; its fire is the debounced
	// trigger. Start drained so it never fires before the first event.
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	pending := false

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-fw.Events:
			if !ok {
				return
			}
			// Chmod-only events (atime/mtime touches, permission changes)
			// don't change source content — ignore them.
			if ev.Op == fsnotify.Chmod {
				continue
			}
			if rw.skipEvent(ev.Name) {
				continue
			}
			pending = true
			timer.Reset(rw.debounce)

		case err, ok := <-fw.Errors:
			if !ok {
				return
			}
			rw.logger.Warn("auto-reindex: fs watch error", "repo", rw.repoID, "err", err)

		case <-timer.C:
			if !pending {
				continue
			}
			pending = false
			rw.reindex(ctx, "change")
			// New directories may have appeared during the edit; cover them.
			rw.syncWatches(fw, watched)
		}
	}
}

// skipEvent filters events the watcher should never act on: writes inside
// vor's own .vor state dir (the reindex writes wiki.db there — acting on it
// would loop) and the .git internals.
func (rw *repoWatcher) skipEvent(absPath string) bool {
	rel, err := filepath.Rel(rw.root, absPath)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	switch {
	case rel == ".vor" || strings.HasPrefix(rel, ".vor/"):
		return true
	case rel == ".git" || strings.HasPrefix(rel, ".git/"):
		return true
	default:
		return false
	}
}

// syncWatches walks the repo with the traverser (honoring .gitignore /
// .vorIgnore / blocked dirs) and adds an fsnotify watch on every directory
// that holds an includable file, plus the repo root. Directories already
// watched are skipped. fsnotify on Linux/macOS is non-recursive, so we
// watch each directory explicitly.
func (rw *repoWatcher) syncWatches(fw *fsnotify.Watcher, watched map[string]bool) {
	dirs := rw.sourceDirs()
	for dir := range dirs {
		if watched[dir] {
			continue
		}
		if err := fw.Add(dir); err != nil {
			rw.logger.Warn("auto-reindex: cannot watch dir", "repo", rw.repoID, "dir", dir, "err", err)
			continue
		}
		watched[dir] = true
	}
}

// sourceDirs returns the set of directories containing includable files,
// plus the repo root. Reusing the traverser keeps the watch set in lockstep
// with what the pipeline actually indexes.
func (rw *repoWatcher) sourceDirs() map[string]bool {
	dirs := map[string]bool{rw.root: true}
	tr, err := traverser.New(traverser.Options{RepoRoot: rw.root})
	if err != nil {
		rw.logger.Warn("auto-reindex: traverser init failed", "repo", rw.repoID, "err", err)
		return dirs
	}
	// A fresh, bounded context for the walk — independent of any reindex.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for fi := range tr.Traverse(ctx) {
		dirs[filepath.Dir(fi.AbsPath)] = true
	}
	return dirs
}

// reindex runs one incremental pipeline pass, skipping if a run is already
// in flight for this repo (the MCP vor_reindex tool can fire concurrently).
// reason is logged for observability ("startup" or "change").
func (rw *repoWatcher) reindex(ctx context.Context, reason string) {
	pstore := pipelinestore.New(rw.db)
	if latest, err := pstore.LatestRun(ctx, rw.repoID); err == nil && latest != nil &&
		latest.Overall == pipelinestore.OutcomeRunning {
		rw.logger.Debug("auto-reindex: run already in progress, skipping",
			"repo", rw.repoID, "reason", reason)
		return
	}

	rw.logger.Info("auto-reindex: starting", "repo", rw.repoID, "reason", reason)
	start := time.Now()
	if _, err := pipeline.Run(ctx, pipeline.Options{
		RepoPath:     rw.root,
		Mode:         pipeline.ModeUpdate,
		DB:           rw.db,
		RepositoryID: rw.repoID,
		RunID:        uuid.NewString(),
		Logger:       rw.logger,
	}); err != nil {
		if ctx.Err() != nil {
			return // shutdown cancelled the run; not an error worth shouting about
		}
		rw.logger.Error("auto-reindex: failed", "repo", rw.repoID, "reason", reason, "err", err)
		return
	}
	rw.logger.Info("auto-reindex: done", "repo", rw.repoID, "reason", reason,
		"took", time.Since(start).Round(time.Millisecond))
}
