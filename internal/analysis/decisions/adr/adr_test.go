package adr_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/decisions"
	_ "github.com/repowise-dev/repowise-go/internal/analysis/decisions/adr"
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

func TestADR_ExtractsTitleStatusDecision(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "docs/adr/0001-use-jwt.md", `# Use JWT for API auth

## Status
Accepted

## Context
The API is stateless and runs on Kubernetes horizontal pods.

## Decision
We will use JWT bearer tokens for API authentication.

## Consequences
- Tokens contain user identity, no DB lookup per request
- Token revocation requires a denylist (operational cost)
`)
	got, err := run(t, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 ADR, got %d", len(got))
	}
	r := got[0]
	if r.Title != "Use JWT for API auth" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.Status != "active" {
		t.Errorf("Status = %q, want 'active' (accepted → active)", r.Status)
	}
	if r.Decision == "" {
		t.Errorf("Decision empty")
	}
	if len(r.Consequences) != 2 {
		t.Errorf("Consequences = %v, want 2 bullets", r.Consequences)
	}
	if r.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want 0.9", r.Confidence)
	}
	if r.EvidenceFile != "docs/adr/0001-use-jwt.md" {
		t.Errorf("EvidenceFile = %q", r.EvidenceFile)
	}
}

func TestADR_RecognisesMultipleConventions(t *testing.T) {
	tmp := t.TempDir()
	// All three common ADR directories.
	writeFile(t, tmp, "docs/adr/0001-a.md", "# Decision A\n## Decision\nDo A")
	writeFile(t, tmp, "doc/adr/0002-b.md", "# Decision B\n## Decision\nDo B")
	writeFile(t, tmp, "docs/architecture/decisions/0003-c.md", "# Decision C\n## Decision\nDo C")

	got, err := run(t, tmp)
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, r := range got {
		titles = append(titles, r.Title)
	}
	slices.Sort(titles)
	want := []string{"Decision A", "Decision B", "Decision C"}
	if !slices.Equal(titles, want) {
		t.Errorf("titles = %v, want %v", titles, want)
	}
}

func TestADR_StatusNormalisation(t *testing.T) {
	cases := []struct {
		raw, want string
	}{
		{"Accepted", "active"},
		{"ACTIVE", "active"},
		{"Proposed", "proposed"},
		{"Rejected", "deprecated"},
		{"Deprecated", "deprecated"},
		{"Superseded by 0042", "superseded"},
		{"???", decisions.DefaultStatus},
	}
	for _, tc := range cases {
		tmp := t.TempDir()
		writeFile(t, tmp, "docs/adr/0001-x.md", "# X\n## Status\n"+tc.raw+"\n## Decision\nx")
		got, _ := run(t, tmp)
		if len(got) != 1 || got[0].Status != tc.want {
			t.Errorf("Status raw=%q → %q, want %q", tc.raw, got[0].Status, tc.want)
		}
	}
}

func TestADR_NoTitleUsesFilenameStem(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "docs/adr/use-postgres.md", "## Decision\nUse Postgres for OLTP\n")
	got, _ := run(t, tmp)
	if len(got) != 1 {
		t.Fatalf("got %d ADRs", len(got))
	}
	if got[0].Title != "use-postgres" {
		t.Errorf("Title fallback = %q, want stem", got[0].Title)
	}
}

func TestADR_EmptyDocumentSkipped(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "docs/adr/0001-empty.md", "")
	got, _ := run(t, tmp)
	if len(got) != 0 {
		t.Errorf("empty doc should be skipped: %+v", got)
	}
}

func TestADR_NonADRDirsIgnored(t *testing.T) {
	tmp := t.TempDir()
	// Looks like an ADR but lives in a random directory.
	writeFile(t, tmp, "random/0001-foo.md", "# Decision\n## Decision\nfoo")
	got, _ := run(t, tmp)
	if len(got) != 0 {
		t.Errorf("non-ADR-dir markdown was picked up: %+v", got)
	}
}

func run(t *testing.T, repoRoot string) ([]decisions.Record, error) {
	t.Helper()
	e := decisions.Lookup(decisions.SourceADR)
	if e == nil {
		t.Fatal("adr extractor not registered")
	}
	return e.Extract(context.Background(), decisions.Input{RepoRoot: repoRoot})
}
