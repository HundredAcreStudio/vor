// Package inline mines architectural decisions from comment markers
// embedded in source code. The supported markers are:
//
//	DECISION:  the call made
//	WHY:       the rationale
//	TRADEOFF:  the cost of the chosen path
//	RATIONALE: synonym for WHY
//
// Markers are matched anywhere on a line — comment syntax varies by
// language and the keywords are unusual enough outside of intentional
// markers that false positives are rare. Each match becomes one
// Record with confidence=1.0 (literal text, not inferred).
//
// The extractor reads each file from disk, scans for markers, and
// stamps the file + line as evidence. The verbatim matched text
// becomes the Record.SourceQuote for the anti-hallucination gate.
package inline

import (
	"context"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/HundredAcreStudio/vor/internal/analysis/decisions"
)

// Extractor implements decisions.Extractor.
type Extractor struct{}

func init() { decisions.Register(&Extractor{}) }

// Source returns the canonical source identifier.
func (Extractor) Source() string { return decisions.SourceInlineMarker }

// markerPattern matches `MARKER: text` anywhere on a line. Case-
// insensitive on the marker keyword; the captured groups are the
// marker name (group 1) and the trimmed body (group 2).
//
// Permissive about leading comment punctuation — works for //, #,
// /*, """, ', etc. without language-specific knowledge.
var markerPattern = regexp.MustCompile(
	`(?i)\b(DECISION|WHY|TRADEOFF|RATIONALE)\s*:\s*(.+?)\s*$`,
)

// Extract walks Input.ParsedFiles, reads each file's content from disk
// (parser output doesn't carry the raw bytes), scans for markers, and
// returns one Record per match.
func (Extractor) Extract(ctx context.Context, in decisions.Input) ([]decisions.Record, error) {
	if len(in.ParsedFiles) == 0 {
		return nil, nil
	}
	now := time.Now()
	out := make([]decisions.Record, 0)
	for _, pf := range in.ParsedFiles {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		records, err := scanFile(pf.FileInfo.AbsPath, pf.FileInfo.Path, now)
		if err != nil {
			// Per-file errors don't abort the whole sweep.
			continue
		}
		out = append(out, records...)
	}
	return out, nil
}

// scanFile reads the file at absPath and returns a Record per marker
// match. Line numbers are 1-indexed to match the rest of the codebase.
func scanFile(absPath, relPath string, now time.Time) ([]decisions.Record, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	src := string(data)
	if !containsMarkerKeyword(src) {
		// Fast path: no marker keyword anywhere, skip the regex.
		return nil, nil
	}

	out := make([]decisions.Record, 0)
	for lineIdx, line := range strings.Split(src, "\n") {
		m := markerPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		kind := strings.ToUpper(m[1])
		if kind == "RATIONALE" {
			kind = "WHY" // canonicalise synonym
		}
		body := strings.TrimSpace(m[2])
		body = strings.TrimRight(body, "*/")
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		// Truncate the assembled title (kind + body) so the total stays
		// in a sensible UI display range. The kind prefix is ~10 chars;
		// 80 bytes total keeps titles on one terminal line in most fonts.
		rec := decisions.Record{
			Title:         truncate(kind+": "+body, 80),
			Status:        decisions.DefaultStatus,
			Source:        decisions.SourceInlineMarker,
			EvidenceFile:  relPath,
			EvidenceLine:  lineIdx + 1,
			SourceQuote:   strings.TrimSpace(line),
			Confidence:    1.0,
			Verification:  decisions.VerificationExact,
			CreatedAt:     now,
			Tags:          []string{strings.ToLower(kind)},
			AffectedFiles: []string{relPath},
		}
		switch kind {
		case "DECISION":
			rec.Decision = body
		case "WHY":
			rec.Rationale = body
		case "TRADEOFF":
			rec.Consequences = []string{body}
		}
		out = append(out, rec)
	}
	return out, nil
}

// containsMarkerKeyword is a substring pre-check so we skip the regex
// for files that obviously have no markers. Significantly speeds up
// the scan on large codebases.
func containsMarkerKeyword(s string) bool {
	for _, kw := range []string{"DECISION:", "WHY:", "TRADEOFF:", "RATIONALE:"} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// truncate clips s to n bytes (well, runes — using bytes is fine for
// titles).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
