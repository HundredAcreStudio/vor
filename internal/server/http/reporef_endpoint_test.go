package http_test

import (
	"net/http"
	"testing"
)

// TestRepoRef_SlugResolves verifies the {repoID} subrouter accepts a slug
// (the fixture repo is named "fix" → slug "fix") and resolves it to the
// canonical id, so a per-repo endpoint works addressed by slug.
func TestRepoRef_SlugResolves(t *testing.T) {
	srv, _ := fixtureRepo(t)

	// Addressed by slug.
	var body struct {
		Counts struct {
			DeadCode int `json:"deadCode"`
		} `json:"counts"`
	}
	hitJSON(t, srv.URL, "/api/repos/fix/risk", &body)
	if body.Counts.DeadCode != 2 {
		t.Errorf("slug-addressed risk deadCode = %d, want 2", body.Counts.DeadCode)
	}

	// The repo detail endpoint reports the slug, also resolvable by slug.
	var detail struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	hitJSON(t, srv.URL, "/api/repos/fix", &detail)
	if detail.Slug != "fix" || detail.Name != "fix" {
		t.Errorf("repo detail slug/name = %q/%q, want fix/fix", detail.Slug, detail.Name)
	}

	// An unknown slug 404s.
	resp := mustGet(t, srv.URL+"/api/repos/nope-not-a-repo/risk")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown slug status = %d, want 404", resp.StatusCode)
	}
}
