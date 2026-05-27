package changelog_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/decisions"
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/changelog"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChangelog_KeepACahangelog(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "CHANGELOG.md", `# Changelog

## [2.0.0] - 2024-06-15

### Changed
- **BREAKING**: rename Provider.Generate to Provider.Complete
- Improved cache hit ratio

### Removed
- BREAKING removal of deprecated repowise_old_status MCP tool

## [1.5.0] - 2024-03-10

### Added
- New repowise_decisions tool

### Fixed
- Stop a panic on empty repos
`)
	got, err := run(t, tmp)
	if err != nil {
		t.Fatal(err)
	}
	// Expect 2 BREAKING records — one per BREAKING bullet in 2.0.0;
	// nothing from 1.5.0 (no BREAKING markers).
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d:\n%+v", len(got), got)
	}
	for _, r := range got {
		if !strings.Contains(r.Title, "2.0.0") {
			t.Errorf("expected 2.0.0 in title, got %q", r.Title)
		}
		if !slices.Contains(r.Tags, "breaking") {
			t.Errorf("expected 'breaking' tag: %v", r.Tags)
		}
		if r.Confidence != 0.85 {
			t.Errorf("Confidence = %v, want 0.85", r.Confidence)
		}
	}
}

func TestChangelog_RecognisesAlternateFilenames(t *testing.T) {
	for _, name := range []string{"CHANGELOG.md", "CHANGES.md", "HISTORY.md"} {
		tmp := t.TempDir()
		writeFile(t, tmp, name, `## [1.0.0] - 2024-01-01
### Changed
- BREAKING: this is a real change
`)
		got, err := run(t, tmp)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(got) != 1 {
			t.Errorf("%s: got %d records, want 1", name, len(got))
		}
	}
}

func TestChangelog_NoBreakingNoRecords(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "CHANGELOG.md", `## [1.0.0] - 2024-01-01
### Added
- a new feature
- another one
### Fixed
- some bug
`)
	got, _ := run(t, tmp)
	if len(got) != 0 {
		t.Errorf("non-breaking entries should not produce records, got %d", len(got))
	}
}

func TestChangelog_MissingFileNoError(t *testing.T) {
	tmp := t.TempDir()
	got, err := run(t, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no records when no CHANGELOG exists, got %d", len(got))
	}
}

func TestChangelog_UnreleasedSection(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "CHANGELOG.md", `## [Unreleased]
### Changed
- BREAKING: rename Provider.Generate to Provider.Complete
`)
	got, _ := run(t, tmp)
	if len(got) != 1 {
		t.Fatalf("expected 1 record from Unreleased, got %d", len(got))
	}
	if !strings.Contains(got[0].Title, "Unreleased") {
		t.Errorf("expected 'Unreleased' in title, got %q", got[0].Title)
	}
}

func TestChangelog_TagsIncludeVersionAndDate(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "CHANGELOG.md", `## [1.0.0] - 2024-01-15
### Changed
- BREAKING: removed legacy endpoint
`)
	got, _ := run(t, tmp)
	if len(got) != 1 {
		t.Fatal("want 1 record")
	}
	if !slices.Contains(got[0].Tags, "1.0.0") {
		t.Errorf("expected '1.0.0' tag: %v", got[0].Tags)
	}
	if !slices.Contains(got[0].Tags, "2024-01-15") {
		t.Errorf("expected '2024-01-15' tag: %v", got[0].Tags)
	}
}

func run(t *testing.T, repoRoot string) ([]decisions.Record, error) {
	t.Helper()
	e := decisions.Lookup(decisions.SourceChangelog)
	if e == nil {
		t.Fatal("changelog extractor not registered")
	}
	return e.Extract(context.Background(), decisions.Input{RepoRoot: repoRoot})
}
