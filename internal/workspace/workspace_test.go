package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/workspace"
)

func TestLoad_EmptyWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	s, err := workspace.Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || len(s.Repos) != 0 {
		t.Errorf("expected empty state, got %+v", s)
	}
}

func TestAdd_FirstEntryBecomesDefault(t *testing.T) {
	s := &workspace.State{}
	if err := s.Add("api", "/repos/api"); err != nil {
		t.Fatal(err)
	}
	if s.DefaultAlias != "api" {
		t.Errorf("default = %q, want api (first entry should default)", s.DefaultAlias)
	}
}

func TestAdd_SecondEntryDoesNotDisplaceDefault(t *testing.T) {
	s := &workspace.State{}
	_ = s.Add("api", "/repos/api")
	if err := s.Add("web", "/repos/web"); err != nil {
		t.Fatal(err)
	}
	if s.DefaultAlias != "api" {
		t.Errorf("default flipped on second add: %q", s.DefaultAlias)
	}
}

func TestAdd_RejectsDuplicateAlias(t *testing.T) {
	s := &workspace.State{}
	_ = s.Add("api", "/repos/api")
	if err := s.Add("api", "/repos/other"); err == nil {
		t.Error("expected error on duplicate alias")
	}
}

func TestAdd_RejectsDuplicatePath(t *testing.T) {
	s := &workspace.State{}
	_ = s.Add("api", "/repos/api")
	if err := s.Add("api2", "/repos/api"); err == nil {
		t.Error("expected error on duplicate path")
	}
}

func TestRemove_UpdatesDefault(t *testing.T) {
	s := &workspace.State{}
	_ = s.Add("api", "/repos/api")
	_ = s.Add("web", "/repos/web")
	if err := s.Remove("api"); err != nil {
		t.Fatal(err)
	}
	if s.DefaultAlias != "web" {
		t.Errorf("default after removing default = %q, want web", s.DefaultAlias)
	}
}

func TestRemove_EmptiesDefaultWhenLast(t *testing.T) {
	s := &workspace.State{}
	_ = s.Add("api", "/repos/api")
	_ = s.Remove("api")
	if s.DefaultAlias != "" {
		t.Errorf("default after removing last entry = %q, want empty", s.DefaultAlias)
	}
}

func TestRemove_MissingAliasErrors(t *testing.T) {
	s := &workspace.State{}
	if err := s.Remove("ghost"); err == nil {
		t.Error("expected error for missing alias")
	}
}

func TestSetDefault(t *testing.T) {
	s := &workspace.State{}
	_ = s.Add("api", "/repos/api")
	_ = s.Add("web", "/repos/web")
	if err := s.SetDefault("web"); err != nil {
		t.Fatal(err)
	}
	if s.DefaultAlias != "web" {
		t.Errorf("default = %q, want web", s.DefaultAlias)
	}
}

func TestSetDefault_MissingAlias(t *testing.T) {
	s := &workspace.State{}
	_ = s.Add("api", "/repos/api")
	if err := s.SetDefault("ghost"); err == nil {
		t.Error("expected error for missing alias")
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	tmp := t.TempDir()
	original := &workspace.State{
		Repos: []workspace.Entry{
			{Alias: "api", Path: "/repos/api"},
			{Alias: "web", Path: "/repos/web"},
		},
		DefaultAlias: "web",
	}
	if err := original.Save(tmp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".repowise", "workspace.json")); err != nil {
		t.Fatalf("workspace.json not created: %v", err)
	}
	loaded, err := workspace.Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DefaultAlias != "web" {
		t.Errorf("default round-trip lost: %q", loaded.DefaultAlias)
	}
	if len(loaded.Repos) != 2 {
		t.Errorf("repos round-trip lost: %+v", loaded.Repos)
	}
}

func TestDefaultAliasFromPath(t *testing.T) {
	cases := map[string]string{
		"/repos/my-api":      "my-api",
		"/repos/My API":      "my-api",
		"/repos/foo_bar_baz": "foo-bar-baz",
		"/repos/v3.0.0/svc":  "svc",
		"/repos/123-svc":     "123-svc",
	}
	for in, want := range cases {
		if got := workspace.DefaultAliasFromPath(in); got != want {
			t.Errorf("DefaultAliasFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}
