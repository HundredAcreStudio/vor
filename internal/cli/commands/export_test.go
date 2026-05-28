package commands_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
)

func TestExport_NoPagesYet(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runVorCmd(t, nil, "export", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "no pages found") {
		t.Errorf("expected 'no pages found' message, got %q", stdout)
	}
}

// exportFixture: standard repo fixture + 2 seeded wiki_pages.
func exportFixture(t *testing.T) (string, string) {
	t.Helper()
	tmp, _, conn := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	repoRow, _ := repos.New(conn).EnsureByLocalPath(context.Background(), tmp, "")
	ws := wikistore.New(conn)
	if _, err := ws.Upsert(context.Background(), models.Page{
		RepositoryID: repoRow.ID, PageType: models.PageKindFileOverview,
		Title:      "main.go overview",
		Content:    "# main.go\n\nThe entrypoint.\n",
		Summary:    "Entrypoint",
		TargetPath: "main.go",
		SourceHash: "h1", ModelName: "mock-1", ProviderName: "mock",
		InputTokens: 5, OutputTokens: 2, Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Upsert(context.Background(), models.Page{
		RepositoryID: repoRow.ID, PageType: models.PageKindSymbolDetail,
		Title:      "main:Sym",
		Content:    "# Sym\n\nA function.\n",
		Summary:    "fn",
		TargetPath: "main.go::Sym",
		SourceHash: "h2", ModelName: "mock-1", ProviderName: "mock",
		InputTokens: 3, OutputTokens: 1, Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
	return tmp, repoRow.ID
}

func TestExport_MarkdownDefault(t *testing.T) {
	tmp, _ := exportFixture(t)
	if _, _, err := runVorCmd(t, nil, "export", tmp); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, ".vor", "export")
	files, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d (%v)", len(files), files)
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".md") {
			t.Errorf("non-.md file in export dir: %q", f.Name())
		}
	}
}

func TestExport_FilenameSanitisation(t *testing.T) {
	tmp, _ := exportFixture(t)
	if _, _, err := runVorCmd(t, nil, "export", tmp); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, ".vor", "export")
	// main.go::Sym should be sanitised to main.go__Sym.md (:: → __)
	if _, err := os.Stat(filepath.Join(out, "main.go__Sym.md")); err != nil {
		t.Errorf("expected main.go__Sym.md to exist (filename sanitisation): %v", err)
	}
}

func TestExport_HTML(t *testing.T) {
	tmp, _ := exportFixture(t)
	if _, _, err := runVorCmd(t, nil, "export", "--format", "html", tmp); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, ".vor", "export")
	files, _ := os.ReadDir(out)
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".html") {
			t.Errorf("non-.html file: %q", f.Name())
		}
	}
	body, _ := os.ReadFile(filepath.Join(out, "main.go.html"))
	s := string(body)
	for _, want := range []string{
		"<!DOCTYPE html>",
		"<title>main.go overview</title>",
		"<h1>main.go overview</h1>",
		"&lt;",
	} {
		_ = want
	}
	if !strings.Contains(s, "<h1>main.go overview</h1>") {
		t.Errorf("HTML missing expected heading: %q", s)
	}
}

func TestExport_JSON(t *testing.T) {
	tmp, _ := exportFixture(t)
	if _, _, err := runVorCmd(t, nil, "export", "--format", "json", tmp); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(tmp, ".vor", "export", "wiki_pages.json"))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Pages []map[string]any `json:"pages"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(env.Pages) != 2 {
		t.Errorf("expected 2 pages in JSON, got %d", len(env.Pages))
	}
}

func TestExport_FullJSON(t *testing.T) {
	tmp, _ := exportFixture(t)
	if _, _, err := runVorCmd(t, nil, "export", "--format", "json", "--full", tmp); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(tmp, ".vor", "export", "wiki_pages.json"))
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"pages", "decisions", "dead_code", "hotspots"} {
		if _, ok := env[key]; !ok {
			t.Errorf("expected key %q in --full envelope", key)
		}
	}
	// Per-page should now include confidence + freshness_status.
	pages := env["pages"].([]any)
	first := pages[0].(map[string]any)
	if _, ok := first["confidence"]; !ok {
		t.Error("--full pages should include confidence")
	}
	if _, ok := first["freshness_status"]; !ok {
		t.Error("--full pages should include freshness_status")
	}
}

func TestExport_CustomOutputDir(t *testing.T) {
	tmp, _ := exportFixture(t)
	customOut := filepath.Join(tmp, "custom-export")
	if _, _, err := runVorCmd(t, nil, "export", "-o", customOut, tmp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(customOut); err != nil {
		t.Errorf("custom dir not created: %v", err)
	}
	files, _ := os.ReadDir(customOut)
	if len(files) != 2 {
		t.Errorf("expected 2 files in custom dir, got %d", len(files))
	}
}

func TestExport_UnknownFormatErrors(t *testing.T) {
	tmp, _ := exportFixture(t)
	_, _, err := runVorCmd(t, nil, "export", "--format", "wat", tmp)
	if err == nil {
		t.Error("expected error for unknown format")
	}
}
