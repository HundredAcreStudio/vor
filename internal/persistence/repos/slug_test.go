package repos_test

import (
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"vor":         "vor",
		"My Repo":     "my-repo",
		"foo_bar.baz": "foo-bar-baz",
		"  Spaced  ":  "spaced",
		"--weird--":   "weird",
		"":            "repo",
		"@@@":         "repo",
		"Repo123":     "repo123",
	}
	for in, want := range cases {
		if got := repos.Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueSlugs_DisambiguatesCollisions(t *testing.T) {
	rs := []repos.Repository{
		{ID: "aaaaaa1111", Name: "vor"},
		{ID: "bbbbbb2222", Name: "Vor"}, // same slug as the first
		{ID: "cccccc3333", Name: "other"},
	}
	m := repos.UniqueSlugs(rs)
	if m["aaaaaa1111"] != "vor" {
		t.Errorf("first 'vor' should keep clean slug, got %q", m["aaaaaa1111"])
	}
	if m["bbbbbb2222"] == "vor" || m["bbbbbb2222"] == "" {
		t.Errorf("colliding 'Vor' should be disambiguated, got %q", m["bbbbbb2222"])
	}
	if m["aaaaaa1111"] == m["bbbbbb2222"] {
		t.Error("colliding repos must get distinct slugs")
	}
	if m["cccccc3333"] != "other" {
		t.Errorf("non-colliding slug wrong: %q", m["cccccc3333"])
	}
}
