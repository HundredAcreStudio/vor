// Package insights is the shared read layer over the indexed database: the
// analytical queries that power both the HTTP dashboard endpoints and the MCP
// tools (bus factor, attention, risk, communities, …). Each function takes a
// context, an open *sql.DB, and a repository id, and returns typed values with
// JSON tags so callers can serialize them directly. Keeping the SQL here means
// the dashboard and the coding agent answer from exactly the same logic.
package insights

import "strings"

// moduleKey collapses a file path to its first two segments (e.g.
// "internal/server"), or one segment when that's all there is. Used to bucket
// files into coarse modules for the dependency matrix and architecture views.
func moduleKey(path string) string {
	parts := strings.SplitN(path, "/", 3)
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// dominantDir returns the most common top-level directory across paths, used
// to label a graph community (e.g. "internal", "ui"). Falls back to "—".
func dominantDir(paths []string) string {
	counts := map[string]int{}
	for _, p := range paths {
		seg := p
		if i := strings.IndexByte(p, '/'); i >= 0 {
			seg = p[:i]
		}
		counts[seg]++
	}
	best, bestN := "—", 0
	for seg, n := range counts {
		if n > bestN {
			best, bestN = seg, n
		}
	}
	return best
}

// filePathOf strips a symbol suffix ("path::Sym") to the file path. For file
// nodes the node_id is already the path, so this is a no-op there.
func filePathOf(target string) string {
	if i := strings.Index(target, "::"); i >= 0 {
		return target[:i]
	}
	return target
}
