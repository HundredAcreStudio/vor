// Package adr mines Architectural Decision Records (ADRs) from markdown
// files. Recognises both Nygard and MADR conventions:
//
//	docs/adr/0001-use-postgres.md
//	docs/architecture/decisions/0042-event-sourcing.md
//	doc/adr/...
//
// Files matching the ADR-naming convention are parsed for their title
// (first H1), status (from "## Status" section if present), context
// ("## Context"), decision ("## Decision"), and consequences
// ("## Consequences"). Each ADR file produces exactly one Record with
// Source=adr.
//
// Lower-confidence than inline markers (0.9 vs 1.0) because ADR titles
// are author-supplied prose and the section extraction is heuristic.
package adr

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/repowise-dev/repowise-go/internal/analysis/decisions"
)

// Extractor is the ADR scanner.
type Extractor struct{}

func init() { decisions.Register(&Extractor{}) }

// Source returns the canonical source identifier.
func (Extractor) Source() string { return decisions.SourceADR }

// adrDirGlobs are the common ADR directory conventions. Files outside
// these directories aren't considered even if they have ADR-looking
// names. Keeps the scan tight.
var adrDirGlobs = []string{
	"docs/adr",
	"doc/adr",
	"docs/architecture/decisions",
	"adr",
}

// adrFilePattern matches typical ADR filenames: "0001-title.md",
// "ADR-042-title.md", "title.md" inside an ADR directory.
var adrFilePattern = regexp.MustCompile(`(?i)^(adr-?)?(\d+)?-?.+\.md$`)

// Extract walks the well-known ADR directories under Input.RepoRoot and
// parses each .md file into a Record.
func (Extractor) Extract(ctx context.Context, in decisions.Input) ([]decisions.Record, error) {
	if in.RepoRoot == "" {
		return nil, nil
	}
	now := time.Now()
	out := make([]decisions.Record, 0)
	for _, dir := range adrDirGlobs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		full := filepath.Join(in.RepoRoot, dir)
		_ = filepath.WalkDir(full, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if d.IsDir() {
				return nil
			}
			base := filepath.Base(path)
			if !adrFilePattern.MatchString(base) {
				return nil
			}
			rec, ok := parseADRFile(path, in.RepoRoot, now)
			if ok {
				out = append(out, rec)
			}
			return nil
		})
	}
	return out, nil
}

// parseADRFile reads a single ADR file and returns its Record. Returns
// ok=false if the file is empty or unreadable.
func parseADRFile(absPath, repoRoot string, now time.Time) (decisions.Record, bool) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return decisions.Record{}, false
	}
	src := string(data)
	if strings.TrimSpace(src) == "" {
		return decisions.Record{}, false
	}

	rel, err := filepath.Rel(repoRoot, absPath)
	if err != nil || rel == "" {
		rel = absPath
	}
	rel = filepath.ToSlash(rel)

	title := extractTitle(src)
	if title == "" {
		// Use the filename stem as fallback title.
		title = strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
	}

	sections := splitSections(src)

	rec := decisions.Record{
		Title:         title,
		Source:        decisions.SourceADR,
		Status:        normaliseStatus(sections["status"]),
		Context:       sections["context"],
		Decision:      sections["decision"],
		Rationale:     sections["rationale"], // some ADR templates use "Rationale" instead
		Consequences:  splitBullets(sections["consequences"]),
		EvidenceFile:  rel,
		EvidenceLine:  1,
		SourceQuote:   truncateLine(extractTitle(src)),
		Confidence:    0.9,
		Verification:  decisions.VerificationExact,
		CreatedAt:     now,
		Tags:          []string{"adr"},
		AffectedFiles: []string{rel},
	}

	// If both Decision and Rationale are empty, fall back to using the
	// document body so the record isn't blank.
	if rec.Decision == "" && rec.Rationale == "" {
		body := bodyAfterTitle(src)
		if len(body) > 500 {
			body = body[:500] + "…"
		}
		rec.Decision = body
	}
	return rec, true
}

// extractTitle returns the first H1 from the markdown, with "#" stripped.
func extractTitle(src string) string {
	for _, line := range strings.SplitN(src, "\n", 64) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		}
	}
	return ""
}

// bodyAfterTitle returns the source minus the first H1 line.
func bodyAfterTitle(src string) string {
	idx := strings.Index(src, "\n")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(src[idx+1:])
}

// splitSections returns a lowercase-keyed map of section name → body.
// Recognises H2 ("## Status") and H3 ("### Status") headers.
func splitSections(src string) map[string]string {
	out := map[string]string{}
	var current string
	var buf strings.Builder
	flush := func() {
		if current == "" {
			return
		}
		out[strings.ToLower(current)] = strings.TrimSpace(buf.String())
		buf.Reset()
	}
	for line := range strings.SplitSeq(src, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## "):
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(trimmed, "##"))
		case strings.HasPrefix(trimmed, "### "):
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(trimmed, "###"))
		default:
			if current != "" {
				buf.WriteString(line)
				buf.WriteByte('\n')
			}
		}
	}
	flush()
	return out
}

// normaliseStatus maps ADR status text to the schema's enum:
//
//	"accepted", "active"     → "active"
//	"proposed"               → "proposed"
//	"deprecated", "rejected" → "deprecated"
//	"superseded"             → "superseded"
//
// Anything else falls through to DefaultStatus.
func normaliseStatus(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case s == "":
		return decisions.DefaultStatus
	case strings.Contains(s, "supersed"):
		return "superseded"
	case strings.Contains(s, "accept"), strings.Contains(s, "active"):
		return "active"
	case strings.Contains(s, "propos"):
		return "proposed"
	case strings.Contains(s, "deprecat"), strings.Contains(s, "reject"):
		return "deprecated"
	default:
		return decisions.DefaultStatus
	}
}

// splitBullets returns the bullet-list items as a []string. Lines
// starting with "- " or "* " are bullets; anything else becomes one
// pseudo-bullet of the whole body.
func splitBullets(body string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	out := make([]string, 0)
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "- "):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
		case strings.HasPrefix(trimmed, "* "):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, "*")))
		}
	}
	if len(out) == 0 {
		return []string{body}
	}
	return out
}

// truncateLine clips s to one line, max 200 bytes.
func truncateLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
