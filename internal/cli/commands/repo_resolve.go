package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

// readRepoOptions captures the flags every read command uses to pick
// the repository it targets. Single source of truth so adding a new
// addressing dimension (alias from workspace, URL, ...) is a one-file
// change.
type readRepoOptions struct {
	// Path comes from --repo (default ".") and is the historical
	// addressing mode — `EnsureByLocalPath` creates a row on miss so
	// running a read command in an un-indexed repo just shows zero
	// counts rather than erroring out.
	Path string
	// ID comes from --repo-id. When non-empty it short-circuits the
	// path lookup and addresses the row directly. Read-only — does
	// NOT create a new row on miss.
	ID string
}

// resolveReadRepo translates a (path | id) pair into a *Repository row.
// When opts.ID is set we do a read-only lookup that errors on miss;
// otherwise we fall back to the existing EnsureByLocalPath path-based
// resolution.
//
// Used by every read CLI command (status, costs, decisions, dead-code,
// health, hotspots, pages, pipeline, externals, search) so the
// addressing semantics are identical across the surface.
func resolveReadRepo(ctx context.Context, conn *sql.DB, opts readRepoOptions) (*repos.Repository, error) {
	store := repos.New(conn)
	if opts.ID != "" {
		r, err := store.Get(ctx, opts.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("no repository with id %s (try `vor repos list`)", opts.ID)
			}
			return nil, err
		}
		return r, nil
	}
	return store.EnsureByLocalPath(ctx, absRepoPath(opts.Path), "")
}

// repoIDFlagDesc is the shared usage string for --repo-id, kept here
// so the wording stays consistent across commands.
const repoIDFlagDesc = "repository id (overrides --repo path lookup; useful when serving multiple repos from one shared DB)"
