// vor-augment is the Claude Code PostToolUse hook. It reads a hook payload on
// stdin and, when the agent's Grep/Glob/Bash output missed something the index
// knows, emits a one-shot `additionalContext` enrichment on stdout.
//
// Faithful to repowise's augment design: it fires on every Grep/Glob/Bash but
// stays SILENT in the common case, speaking up only for asymmetric value —
//
//   - Grep/Glob, 0 results          → rescue: closest indexed symbol/page.
//   - Grep/Glob, large result flood → triage: top files by PageRank.
//   - Grep/Glob, focused result     → silent.
//   - Bash, successful git commit   → notice if the index is stale.
//
// Hooks must never disrupt the agent: any error (bad payload, no DB, no repo)
// exits 0 with empty stdout, and a panic is recovered the same way.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/insights"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/userconfig"
)

const (
	triageThreshold = 15 // grep result lines before we surface a ranking
	triageTopN      = 3
)

type hookPayload struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     toolInput       `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	ToolOutput    json.RawMessage `json:"tool_output"`
	Cwd           string          `json:"cwd"`
}

type toolInput struct {
	Pattern string `json:"pattern"`
	Command string `json:"command"`
}

func main() {
	// Hooks must never crash the agent: recover any panic and emit nothing.
	out := safeRun()
	if out != "" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "PostToolUse",
				"additionalContext": out,
			},
		})
	}
}

func safeRun() (out string) {
	defer func() {
		if recover() != nil {
			out = ""
		}
	}()
	return run()
}

func run() string {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	if err != nil || len(raw) == 0 {
		return ""
	}
	var p hookPayload
	if json.Unmarshal(raw, &p) != nil || p.HookEventName != "PostToolUse" {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := openDB(ctx)
	if err != nil {
		return ""
	}
	defer conn.Close()

	repoID, repoRoot := resolveRepo(ctx, conn, p.Cwd)
	if repoID == "" {
		return ""
	}

	switch p.ToolName {
	case "Grep", "Glob":
		return searchEnrich(ctx, conn, repoID, p)
	case "Bash":
		return bashStaleness(ctx, conn, repoID, repoRoot, p)
	}
	return ""
}

func openDB(ctx context.Context) (*sql.DB, error) {
	conn, _, err := db.Open(ctx, db.OpenOptions{URL: config.LoadBootstrap().DatabaseURL})
	return conn, err
}

// resolveRepo finds the indexed repo whose local_path is cwd or an ancestor
// of it (longest match wins). Returns ("", "") when cwd is outside any repo.
func resolveRepo(ctx context.Context, conn *sql.DB, cwd string) (id, root string) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", ""
	}
	rows, err := conn.QueryContext(ctx, `SELECT id, local_path FROM repositories`)
	if err != nil {
		return "", ""
	}
	defer rows.Close()
	best := -1
	for rows.Next() {
		var rid, lp string
		if rows.Scan(&rid, &lp) != nil || lp == "" {
			continue
		}
		if (abs == lp || strings.HasPrefix(abs, lp+string(filepath.Separator))) && len(lp) > best {
			best, id, root = len(lp), rid, lp
		}
	}
	return id, root
}

// ---- Grep / Glob enrichment ---------------------------------------------

func searchEnrich(ctx context.Context, conn *sql.DB, repoID string, p hookPayload) string {
	pattern := strings.TrimSpace(p.ToolInput.Pattern)
	if pattern == "" || looksLikePath(pattern) {
		return ""
	}
	count := countResults(extractOutput(p))
	switch {
	case count == 0:
		if msg, ok, _ := insights.RescueMatch(ctx, conn, repoID, pattern); ok {
			return msg
		}
		return ""
	case count >= triageThreshold:
		files, _ := insights.TriageFiles(ctx, conn, repoID, pattern, triageTopN)
		if len(files) == 0 {
			return ""
		}
		var b strings.Builder
		fmt.Fprintf(&b, "[vor] %d+ matches for `%s`. Top files by graph centrality:", count, pattern)
		for _, f := range files {
			fmt.Fprintf(&b, "\n  %s", f)
		}
		return b.String()
	default:
		return "" // focused result — the agent already found it
	}
}

// looksLikePath skips literal path/glob lookups, which don't benefit from
// semantic enrichment.
func looksLikePath(pattern string) bool {
	if strings.ContainsAny(pattern, "/\\") {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(pattern))
	for _, ext := range []string{
		".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".kt", ".scala",
		".rb", ".php", ".cs", ".swift", ".cpp", ".cc", ".c", ".h", ".hpp", ".lua",
		".sql", ".yaml", ".yml", ".toml", ".json", ".md",
	} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func extractOutput(p hookPayload) string {
	raw := p.ToolResponse
	if len(raw) == 0 {
		raw = p.ToolOutput
	}
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	for _, k := range []string{"output", "result", "content", "stdout", "text"} {
		if v, ok := obj[k].(string); ok {
			return v
		}
	}
	return ""
}

func countResults(out string) int {
	out = strings.TrimSpace(out)
	if out == "" {
		return 0
	}
	lines := strings.Split(out, "\n")
	head := strings.ToLower(lines[0])
	for _, z := range []string{"no matches found", "no files found", "no files matched", "found 0 "} {
		if strings.Contains(head, z) {
			return 0
		}
	}
	if strings.HasPrefix(head, "found ") && len(lines) > 1 {
		lines = lines[1:]
	}
	n := 0
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

// ---- Bash: stale-index notice -------------------------------------------

var gitMutations = []string{"git commit", "git merge", "git rebase", "git cherry-pick", "git pull"}

func bashStaleness(ctx context.Context, conn *sql.DB, repoID, repoRoot string, p hookPayload) string {
	cmd := p.ToolInput.Command
	mutated := false
	for _, g := range gitMutations {
		if strings.Contains(cmd, g) {
			mutated = true
			break
		}
	}
	if !mutated {
		return ""
	}

	var indexedHead string
	var tracked int
	if err := conn.QueryRowContext(ctx,
		`SELECT COALESCE(head_commit,''), COALESCE(tracked,0) FROM repositories WHERE id = ?`,
		repoID).Scan(&indexedHead, &tracked); err != nil {
		return ""
	}
	// Tracked repos are auto-reindexed by the daemon's watcher — stay quiet.
	if tracked == 1 || indexedHead == "" {
		return ""
	}

	head := gitHead(ctx, repoRoot)
	if head == "" || head == indexedHead {
		return ""
	}
	if alreadyWarned(repoID, head) {
		return ""
	}
	recordWarning(repoID, head)
	return fmt.Sprintf(
		"[vor] Index is stale — last indexed at commit %s, HEAD is now %s. "+
			"Run `vor reindex` (or `vor register .` to have the daemon keep it fresh).",
		short(indexedHead), short(head))
}

func gitHead(ctx context.Context, root string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func short(c string) string {
	if len(c) > 8 {
		return c[:8]
	}
	return c
}

func warnMarker(repoID string) string {
	dir, err := userconfig.StateDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "augment-warned-"+repoID)
}

func alreadyWarned(repoID, head string) bool {
	m := warnMarker(repoID)
	if m == "" {
		return false
	}
	b, err := os.ReadFile(m)
	return err == nil && strings.TrimSpace(string(b)) == head
}

func recordWarning(repoID, head string) {
	if m := warnMarker(repoID); m != "" {
		_ = os.WriteFile(m, []byte(head), 0o644)
	}
}
