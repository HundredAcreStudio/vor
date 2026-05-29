package http_test

import (
	"net/http"
	"strings"
	"testing"

	// Register the wiki task so the tasks registry is non-empty in this test
	// binary (the real binary registers via internal/cli/commands).
	_ "github.com/HundredAcreStudio/vor/internal/generation/wikitask"
)

type taskRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Default    bool   `json:"default"`
	Enabled    bool   `json:"enabled"`
	Overridden bool   `json:"overridden"`
}

type tasksResp struct {
	Tasks              []taskRow `json:"tasks"`
	ProviderConfigured bool      `json:"providerConfigured"`
}

func findTask(rows []taskRow, id string) (taskRow, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return taskRow{}, false
}

func TestTasksEndpoint_ListAndToggle(t *testing.T) {
	// Force no provider so providerConfigured is deterministic regardless of
	// the developer's environment.
	t.Setenv("ANTHROPIC_API_KEY", "")
	srv, repoID := fixtureRepo(t)

	// 1. List: wiki_generation present, default-on, enabled, not overridden.
	var list tasksResp
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/tasks", &list)
	wiki, ok := findTask(list.Tasks, "wiki_generation")
	if !ok {
		t.Fatalf("wiki_generation missing from tasks: %+v", list.Tasks)
	}
	if !wiki.Default || !wiki.Enabled {
		t.Errorf("wiki default=%v enabled=%v, want both true", wiki.Default, wiki.Enabled)
	}
	if wiki.Overridden {
		t.Error("wiki should not be overridden before any toggle")
	}
	if list.ProviderConfigured {
		t.Error("providerConfigured should be false with no API key")
	}

	// 2. Toggle it off for this repo.
	base := srv.URL + "/api/repos/" + repoID + "/tasks/wiki_generation"
	req, _ := http.NewRequest(http.MethodPut, base, strings.NewReader(`{"enabled":false}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}

	// 3. List again: now disabled and marked overridden.
	var list2 tasksResp
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/tasks", &list2)
	wiki2, _ := findTask(list2.Tasks, "wiki_generation")
	if wiki2.Enabled {
		t.Error("wiki should be disabled after toggle")
	}
	if !wiki2.Overridden {
		t.Error("wiki should be marked overridden after toggle")
	}

	// 4. Unknown task → 400.
	req2, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/repos/"+repoID+"/tasks/nope",
		strings.NewReader(`{"enabled":true}`))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("PUT unknown: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown task status = %d, want 400", resp2.StatusCode)
	}
}
