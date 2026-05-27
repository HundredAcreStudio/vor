package inline_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/decisions"
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/inline"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// fixture writes content to a tmp file and returns a ParsedFile that
// points to it. RelPath is the path stamped on records; AbsPath is
// what the extractor reads.
func fixture(t *testing.T, relPath, content string) models.ParsedFile {
	t.Helper()
	dir := t.TempDir()
	abs := filepath.Join(dir, filepath.Base(relPath))
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return models.ParsedFile{
		FileInfo: models.FileInfo{
			Path:    relPath,
			AbsPath: abs,
		},
	}
}

func TestInline_AllMarkerKindsMatched(t *testing.T) {
	pf := fixture(t, "demo/foo.go", `package demo

// DECISION: use channels over mutexes for the job queue
// WHY: lock-free producer-consumer fits this workload better
// TRADEOFF: harder to debug than mutex-based code
// RATIONALE: this is a synonym for WHY

func Hello() {}
`)
	got, err := runExtractor(t, []models.ParsedFile{pf})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d records, want 4: %+v", len(got), got)
	}

	byKind := map[string]decisions.Record{}
	for _, r := range got {
		for _, tag := range r.Tags {
			byKind[tag] = r
		}
	}
	// DECISION populates Decision field.
	if dec := byKind["decision"]; dec.Decision == "" {
		t.Errorf("DECISION marker should populate Decision field: %+v", dec)
	}
	// WHY populates Rationale.
	if why := byKind["why"]; why.Rationale == "" {
		t.Errorf("WHY marker should populate Rationale field: %+v", why)
	}
	// TRADEOFF populates Consequences.
	if to := byKind["tradeoff"]; len(to.Consequences) != 1 {
		t.Errorf("TRADEOFF marker should populate Consequences: %+v", to)
	}
	// RATIONALE is canonicalised to "why".
	whyCount := 0
	for _, r := range got {
		if slices.Contains(r.Tags, "why") {
			whyCount++
		}
	}
	if whyCount != 2 {
		t.Errorf("expected 2 'why' records (WHY + RATIONALE), got %d", whyCount)
	}
}

func TestInline_ConfidenceAndVerification(t *testing.T) {
	pf := fixture(t, "x.py", `# DECISION: keep this synchronous
def main():
    pass
`)
	got, err := runExtractor(t, []models.ParsedFile{pf})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	r := got[0]
	if r.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0 (literal text match)", r.Confidence)
	}
	if r.Verification != decisions.VerificationExact {
		t.Errorf("Verification = %s, want exact", r.Verification)
	}
	if r.Source != decisions.SourceInlineMarker {
		t.Errorf("Source = %s, want inline_marker", r.Source)
	}
}

func TestInline_EvidenceFileAndLine(t *testing.T) {
	pf := fixture(t, "pkg/foo.go", `package foo
// just a comment
// DECISION: pick this path
func A() {}
`)
	got, _ := runExtractor(t, []models.ParsedFile{pf})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].EvidenceFile != "pkg/foo.go" {
		t.Errorf("EvidenceFile = %q, want pkg/foo.go", got[0].EvidenceFile)
	}
	if got[0].EvidenceLine != 3 {
		t.Errorf("EvidenceLine = %d, want 3", got[0].EvidenceLine)
	}
}

func TestInline_AffectedFilesPopulated(t *testing.T) {
	pf := fixture(t, "pkg/x.go", "// DECISION: foo\n")
	got, _ := runExtractor(t, []models.ParsedFile{pf})
	if len(got) != 1 {
		t.Fatalf("want 1 record")
	}
	if !slices.Contains(got[0].AffectedFiles, "pkg/x.go") {
		t.Errorf("AffectedFiles = %v, want to contain pkg/x.go", got[0].AffectedFiles)
	}
}

func TestInline_NoMarkersInPlainCode(t *testing.T) {
	pf := fixture(t, "plain.go", `package plain
// Just a plain comment
// Not a marker line
func Foo() {}
`)
	got, _ := runExtractor(t, []models.ParsedFile{pf})
	if len(got) != 0 {
		t.Errorf("plain code should yield no decisions, got %d", len(got))
	}
}

func TestInline_WorksAcrossCommentSyntaxes(t *testing.T) {
	// /* ... */ block comments, # hash, // slash, and bare keyword
	// should all match.
	pf := fixture(t, "mixed.txt", `
# DECISION: hash-style
// DECISION: slash-style
/* DECISION: block-style */
DECISION: bare keyword
`)
	got, _ := runExtractor(t, []models.ParsedFile{pf})
	if len(got) != 4 {
		t.Errorf("expected 4 matches across comment styles, got %d:\n%+v", len(got), got)
	}
}

func TestInline_LongMarkerTruncatesTitle(t *testing.T) {
	long := "this is a very long marker body that goes on and on, well past eighty characters, no actually it goes way longer than that should ever be allowed in a title field"
	pf := fixture(t, "x.go", "// DECISION: "+long+"\n")
	got, _ := runExtractor(t, []models.ParsedFile{pf})
	if len(got) != 1 {
		t.Fatalf("want 1")
	}
	if len(got[0].Title) > 90 {
		t.Errorf("Title not truncated: %d chars", len(got[0].Title))
	}
	if !strings.Contains(got[0].Title, "…") {
		t.Errorf("expected ellipsis in truncated title: %q", got[0].Title)
	}
}

func TestInline_EmptyMarkerBodySkipped(t *testing.T) {
	pf := fixture(t, "x.go", "// DECISION:    \n// WHY:\n")
	got, _ := runExtractor(t, []models.ParsedFile{pf})
	if len(got) != 0 {
		t.Errorf("empty-body markers should be skipped, got %d", len(got))
	}
}

func TestInline_DedupedAcrossExtractorInvocations(t *testing.T) {
	// Same marker found twice (via running the same engine twice) should
	// produce one Record. (The Engine handles this; the test here
	// verifies dedupe by exercising the registry-level Run.)
	pf := fixture(t, "x.go", "// DECISION: A\n")
	in := decisions.Input{ParsedFiles: []models.ParsedFile{pf, pf}}
	got := decisions.Engine{}.Run(context.Background(), in)
	if len(got) != 1 {
		t.Errorf("dedupe: got %d records for identical input, want 1", len(got))
	}
}

// runExtractor pulls the registered inline extractor and invokes it.
// This is slightly indirect compared to calling the Extractor directly,
// but ensures the registry wiring is tested.
func runExtractor(t *testing.T, files []models.ParsedFile) ([]decisions.Record, error) {
	t.Helper()
	e := decisions.Lookup(decisions.SourceInlineMarker)
	if e == nil {
		t.Fatalf("inline extractor not registered")
	}
	return e.Extract(context.Background(), decisions.Input{ParsedFiles: files})
}
