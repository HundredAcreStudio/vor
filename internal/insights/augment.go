package insights

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// This file backs the vor-augment PostToolUse hook: cheap, read-only lookups
// that enrich an agent's Grep/Glob results with indexed context. Faithful to
// repowise's augment design — value only when the raw tool output missed
// something.

var nonToken = regexp.MustCompile(`[^\w./_-]`)

// cleanToken normalizes a search pattern to a bare identifier-ish token.
func cleanToken(pattern string) string {
	return strings.Trim(nonToken.ReplaceAllString(pattern, ""), "./_-")
}

var camelBoundary = regexp.MustCompile(`(.)([A-Z])`)

// titleASCII upper-cases the first byte of an ASCII identifier word and
// lower-cases the rest (e.g. "yaml" → "Yaml").
func titleASCII(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToLower(s)
	return strings.ToUpper(s[:1]) + s[1:]
}

// nameVariants generates snake/camel/Pascal variants so a literal-grep miss
// (e.g. `parse_yaml`) can still match an indexed `parseYaml` / `ParseYaml`.
func nameVariants(token string) []string {
	token = strings.Trim(token, "_-./")
	if token == "" {
		return nil
	}
	set := map[string]bool{token: true, strings.ToLower(token): true}
	if strings.Contains(token, "_") {
		parts := strings.FieldsFunc(token, func(r rune) bool { return r == '_' })
		if len(parts) > 0 {
			var pascal, camel strings.Builder
			for i, p := range parts {
				c := titleASCII(p)
				pascal.WriteString(c)
				if i == 0 {
					camel.WriteString(strings.ToLower(p))
				} else {
					camel.WriteString(c)
				}
			}
			set[pascal.String()] = true
			set[camel.String()] = true
		}
	}
	snake := strings.ToLower(camelBoundary.ReplaceAllString(token, "${1}_${2}"))
	set[snake] = true

	out := make([]string, 0, len(set))
	for v := range set {
		if v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out) // deterministic
	return out
}

// RescueMatch finds the closest indexed match for a search pattern that
// returned zero results: a fuzzy symbol-name match first, then a wiki page.
// Returns ("", false) when nothing useful is found.
func RescueMatch(ctx context.Context, db *sql.DB, repoID, pattern string) (string, bool, error) {
	clean := cleanToken(pattern)
	if clean == "" {
		return "", false, nil
	}
	variants := nameVariants(clean)
	if len(variants) == 0 {
		return "", false, nil
	}

	clauses := make([]string, 0, len(variants))
	args := []any{repoID}
	for _, v := range variants {
		clauses = append(clauses, "name LIKE ?")
		args = append(args, "%"+v+"%")
	}
	q := `SELECT COALESCE(name,''), COALESCE(kind,'symbol'), node_id, COALESCE(start_line,0)
	        FROM graph_nodes
	       WHERE repository_id = ? AND node_type != 'file' AND COALESCE(name,'') != ''
	         AND (` + strings.Join(clauses, " OR ") + `)
	       LIMIT 20`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return "", false, err
	}
	type sym struct {
		name, kind, file string
		line             int
	}
	var syms []sym
	want := map[string]bool{}
	for _, v := range variants {
		want[strings.ToLower(v)] = true
	}
	for rows.Next() {
		var name, kind, nodeID string
		var line int
		if err := rows.Scan(&name, &kind, &nodeID, &line); err != nil {
			rows.Close()
			return "", false, err
		}
		syms = append(syms, sym{name: name, kind: kind, file: filePathOf(nodeID), line: line})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	if len(syms) > 0 {
		// Rank: exact-variant match first, then shortest name, then file path.
		sort.Slice(syms, func(i, j int) bool {
			ei := want[strings.ToLower(syms[i].name)]
			ej := want[strings.ToLower(syms[j].name)]
			if ei != ej {
				return ei
			}
			if len(syms[i].name) != len(syms[j].name) {
				return len(syms[i].name) < len(syms[j].name)
			}
			return syms[i].file < syms[j].file
		})
		s := syms[0]
		loc := ""
		if s.line > 0 {
			loc = fmt.Sprintf(":%d", s.line)
		}
		extra := ""
		if len(syms) > 1 {
			extra = fmt.Sprintf(" (+%d more)", len(syms)-1)
		}
		return fmt.Sprintf("[vor] No literal match for `%s`. Closest indexed symbol: %s `%s` in %s%s%s",
			pattern, s.kind, s.name, s.file, loc, extra), true, nil
	}

	// Fall back to a generated wiki page whose path/title matches.
	prow := db.QueryRowContext(ctx, `
		SELECT target_path, page_type FROM wiki_pages
		 WHERE repository_id = ? AND page_type = 'file_overview'
		   AND (target_path LIKE ? OR title LIKE ?)
		 LIMIT 1`, repoID, "%"+clean+"%", "%"+clean+"%")
	var target, pageType string
	switch err := prow.Scan(&target, &pageType); err {
	case nil:
		return fmt.Sprintf("[vor] No literal match for `%s`. Wiki has a page for `%s`.", pattern, target), true, nil
	case sql.ErrNoRows:
		return "", false, nil
	default:
		return "", false, err
	}
}

// TriageFiles returns up to limit files (by PageRank) whose path or symbols
// match the pattern — a ranking lens for a flooded search result.
func TriageFiles(ctx context.Context, db *sql.DB, repoID, pattern string, limit int) ([]string, error) {
	clean := cleanToken(pattern)
	if clean == "" {
		return nil, nil
	}
	like := "%" + clean + "%"
	candidates := map[string]bool{}

	// Files containing a matching symbol.
	if rows, err := db.QueryContext(ctx, `
		SELECT node_id FROM graph_nodes
		 WHERE repository_id = ? AND node_type != 'file' AND name LIKE ? LIMIT 100`, repoID, like); err == nil {
		for rows.Next() {
			var nodeID string
			if rows.Scan(&nodeID) == nil {
				candidates[filePathOf(nodeID)] = true
			}
		}
		rows.Close()
	}
	// Files whose path matches.
	if rows, err := db.QueryContext(ctx, `
		SELECT node_id FROM graph_nodes
		 WHERE repository_id = ? AND node_type = 'file' AND node_id LIKE ? LIMIT 100`, repoID, like); err == nil {
		for rows.Next() {
			var nodeID string
			if rows.Scan(&nodeID) == nil {
				candidates[nodeID] = true
			}
		}
		rows.Close()
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// Rank the candidates by PageRank.
	placeholders := make([]string, 0, len(candidates))
	args := []any{repoID}
	for c := range candidates {
		placeholders = append(placeholders, "?")
		args = append(args, c)
	}
	q := `SELECT node_id, pagerank FROM graph_nodes
	       WHERE repository_id = ? AND node_type = 'file' AND node_id IN (` +
		strings.Join(placeholders, ",") + `) ORDER BY pagerank DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var nodeID string
		var pr sql.NullFloat64
		if err := rows.Scan(&nodeID, &pr); err != nil {
			return nil, err
		}
		out = append(out, nodeID)
	}
	return out, rows.Err()
}
