package userconfig_test

import (
	"testing"

	"github.com/HundredAcreStudio/vor/internal/userconfig"
)

func TestWorkspaceRegistry_AddListRemove(t *testing.T) {
	withXDGOverrides(t)
	r, err := userconfig.LoadWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Workspaces) != 0 {
		t.Errorf("expected empty registry, got %+v", r)
	}
	if err := r.Add("/home/u/work", "work"); err != nil {
		t.Fatal(err)
	}
	if err := r.Add("/home/u/side", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Add("/home/u/work", "dup"); err == nil {
		t.Error("expected duplicate add to error")
	}
	if err := userconfig.SaveWorkspaces(r); err != nil {
		t.Fatal(err)
	}

	loaded, err := userconfig.LoadWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Workspaces) != 2 {
		t.Errorf("expected 2 workspaces, got %d", len(loaded.Workspaces))
	}
	// Sorted by path on save.
	if loaded.Workspaces[0].Path != "/home/u/side" {
		t.Errorf("expected sorted order, got %+v", loaded.Workspaces)
	}

	if err := loaded.Remove("/home/u/side"); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Remove("/home/u/missing"); err == nil {
		t.Error("expected error removing nonexistent entry")
	}
	_ = userconfig.SaveWorkspaces(loaded)

	final, _ := userconfig.LoadWorkspaces()
	if len(final.Workspaces) != 1 || final.Workspaces[0].Path != "/home/u/work" {
		t.Errorf("expected one remaining /home/u/work, got %+v", final.Workspaces)
	}
}

func TestWatchedRegistry_RecordWatchAndUpdate(t *testing.T) {
	withXDGOverrides(t)
	r, _ := userconfig.LoadWatched()
	r.RecordWatch("/r/a", "a", "/r")
	r.RecordWatch("/r/b", "b", "/r")
	if len(r.Repos) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(r.Repos))
	}
	for _, repo := range r.Repos {
		if repo.UpdateCount != 0 {
			t.Errorf("brand-new entry should have 0 updates: %+v", repo)
		}
	}

	// Two updates on /r/a, none on /r/b.
	r.RecordUpdate("/r/a")
	r.RecordUpdate("/r/a")
	r.RecordUpdate("/r/missing") // silent no-op

	if err := userconfig.SaveWatched(r); err != nil {
		t.Fatal(err)
	}
	loaded, _ := userconfig.LoadWatched()
	// Sorted by path on save.
	if loaded.Repos[0].Path != "/r/a" {
		t.Errorf("expected /r/a first after sort, got %+v", loaded.Repos)
	}
	if loaded.Repos[0].UpdateCount != 2 {
		t.Errorf("/r/a UpdateCount = %d, want 2", loaded.Repos[0].UpdateCount)
	}
	if loaded.Repos[1].UpdateCount != 0 {
		t.Errorf("/r/b should have 0 updates: %d", loaded.Repos[1].UpdateCount)
	}
}

func TestWatchedRegistry_RecordWatchDoesNotOverwriteAlias(t *testing.T) {
	withXDGOverrides(t)
	r, _ := userconfig.LoadWatched()
	r.RecordWatch("/r/a", "first", "/r1")
	r.RecordWatch("/r/a", "second", "/r2") // second call with different metadata
	if r.Repos[0].Alias != "first" {
		t.Errorf("alias should not be clobbered: got %q", r.Repos[0].Alias)
	}
	if r.Repos[0].WorkspaceRoot != "/r1" {
		t.Errorf("workspace root should not be clobbered: got %q", r.Repos[0].WorkspaceRoot)
	}
}
