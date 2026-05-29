package commands

// Side-effect imports that register post-pipeline tasks. Importing here (the
// package every subcommand lives in) registers them once for the whole vor
// binary — init/reindex run them after indexing, serve's watcher runs them on
// file changes, the HTTP server lists them for the dashboard, and the generate
// command triggers them on demand.
import (
	_ "github.com/HundredAcreStudio/vor/internal/generation/wikitask"
)
