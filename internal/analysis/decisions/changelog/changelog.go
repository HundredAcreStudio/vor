// Package changelog mines decisions from CHANGELOG.md (and HISTORY.md,
// CHANGES.md) in the repo root. Recognises Keep-a-Changelog v1.x format:
//
//   ## [1.0.0] - 2024-01-15
//   ### Added
//   - new feature x
//   ### Changed
//   - **BREAKING**: switched from y to z
//   ### Removed
//   - old foo API
//
// Each release section produces zero or more decision Records depending
// on its sub-sections. Items under "Changed" and "Removed" that mention
// BREAKING or backwards-incompatibility produce one Record; other items
// are dropped (low-signal).
//
// Pass A bias is towards precision: prefer empty over noisy. Future
// passes can opt in to extracting every change as a candidate decision.
package changelog

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/repowise-dev/repowise-go/internal/analysis/decisions"
)

type Extractor struct{}

func init() { decisions.Register(&Extractor{}) }

func (Extractor) Source() string { return decisions.SourceChangelog }

// candidateFilenames lists the names this extractor will read at repo
// root, in the order they're tried. Conventional all-caps forms first
// so the resulting evidence path matches the file as it actually exists
// on disk (avoids casing drift on case-insensitive filesystems like
// macOS APFS, where reading "changelog.md" silently succeeds against
// "CHANGELOG.md").
var candidateFilenames = []string{
	"CHANGELOG.md", "CHANGELOG",
	"HISTORY.md", "HISTORY",
	"CHANGES.md", "CHANGES",
	"NEWS.md", "NEWS",
	"Changelog.md", "Changelog",
	"changelog.md", "changelog",
	"history.md", "history",
	"changes.md", "changes",
	"news.md", "news",
}

// versionHeader matches Keep-a-Changelog section headers:
//   ## [1.0.0] - 2024-01-15
//   ## v1.2.3
//   ## 0.9.0
//   ## [Unreleased]
// Group 1 captures the version label, group 2 the trailing date if any.
var versionHeader = regexp.MustCompile(
	`^##\s*\[?(v?\d+\.\d+\.\d+(?:[-+][\w.\-]+)?|Unreleased|UNRELEASED)\]?(?:\s*[-–—]\s*(.*))?$`,
)

// breakingPattern matches BREAKING / backwards-incompat keywords.
var breakingPattern = regexp.MustCompile(`(?i)\b(BREAKING|BC[\s-]?BREAK|BREAKS|INCOMPATIBLE)\b`)

func (Extractor) Extract(ctx context.Context, in decisions.Input) ([]decisions.Record, error) {
	if in.RepoRoot == "" {
		return nil, nil
	}
	for _, name := range candidateFilenames {
		data, err := os.ReadFile(filepath.Join(in.RepoRoot, name))
		if err == nil {
			return parseChangelog(string(data), filepath.ToSlash(name), time.Now()), nil
		}
	}
	return nil, nil
}

// parseChangelog walks the markdown, partitions it into release
// sections, and emits one Record per BREAKING/incompatible bullet.
func parseChangelog(src, relPath string, now time.Time) []decisions.Record {
	out := make([]decisions.Record, 0)
	var currentVersion, currentDate string

	for i, line := range strings.Split(src, "\n") {
		// Track version headers.
		if m := versionHeader.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			currentVersion = m[1]
			currentDate = ""
			if len(m) > 2 {
				currentDate = strings.TrimSpace(m[2])
			}
			continue
		}
		// Pick up BREAKING bullets within the current section.
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "* ") {
			continue
		}
		if !breakingPattern.MatchString(trimmed) {
			continue
		}
		body := strings.TrimSpace(strings.TrimLeft(trimmed, "-* "))
		if body == "" || currentVersion == "" {
			continue
		}
		title := "BREAKING (" + currentVersion + "): " + truncate(body, 60)
		rec := decisions.Record{
			Title:         title,
			Source:        decisions.SourceChangelog,
			Status:        decisions.DefaultStatus,
			Decision:      body,
			EvidenceFile:  relPath,
			EvidenceLine:  i + 1,
			SourceQuote:   trimmed,
			Confidence:    0.85,
			Verification:  decisions.VerificationExact,
			CreatedAt:     now,
			Tags:          []string{"changelog", "breaking", currentVersion},
		}
		if currentDate != "" {
			rec.Tags = append(rec.Tags, currentDate)
		}
		out = append(out, rec)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
