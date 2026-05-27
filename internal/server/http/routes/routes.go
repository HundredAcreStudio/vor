// Package routes mounts the per-domain route packages. Each route file
// (repos.go, graph.go, hotspots.go, ...) registers handlers on a chi
// router supplied by server.New. Keeping the mounts here lets the server
// package stay short and lets new routes land without touching it.
package routes

import (
	"database/sql"
	"log/slog"
)

// Deps is the bag of dependencies handlers need. Adding new dependencies
// here (e.g. a vector store, a provider registry, a job executor) keeps
// the handler signatures stable.
type Deps struct {
	DB     *sql.DB
	Logger *slog.Logger
}
