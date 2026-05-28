package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/decisions"
	"github.com/HundredAcreStudio/vor/internal/persistence/decisionstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

// seedDecision uses the store directly to put a record into the test
// repo, returning the new id. Used by the show/confirm/dismiss tests.
func seedDecision(t *testing.T, tmp string, title string) string {
	t.Helper()
	_, _, conn := repoFixture(t)
	_ = tmp
	repoRow, _ := repos.New(conn).EnsureByLocalPath(context.Background(), tmp, "")
	id, err := decisionstore.New(conn).Insert(context.Background(), repoRow.ID, decisions.Record{
		Title:        title,
		Decision:     "use X",
		Rationale:    "Y is too slow",
		Status:       "active",
		Confidence:   0.8,
		Verification: decisions.VerificationFuzzy,
		Source:       "cli",
		Tags:         []string{"perf"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDecision_AddAndList(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runVorCmd(t, nil, "decision", "add",
		"--title", "Pin Go to 1.24",
		"--decision", "Use Go 1.24 in CI",
		"--rationale", "iter package needs 1.24",
		"--tags", "build,ci",
		"--repo", tmp,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "added decision") {
		t.Errorf("add output = %q", stdout)
	}
	listOut, _, err := runVorCmd(t, nil, "decision", "list", "--repo", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOut, "Pin Go to 1.24") {
		t.Errorf("list missing new decision: %q", listOut)
	}
}

func TestDecisions_PluralAlias(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runVorCmd(t, nil, "decision", "add",
		"--title", "X", "--repo", tmp); err != nil {
		t.Fatal(err)
	}
	// The plural `decisions` should still work as a group alias.
	stdout, _, err := runVorCmd(t, nil, "decisions", "list", "--repo", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "X") {
		t.Errorf("decisions alias list = %q", stdout)
	}
}

func TestDecision_Show(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	id := seedDecision(t, tmp, "Show me")
	stdout, _, err := runVorCmd(t, nil, "decision", "show", id[:8], "--repo", tmp)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	for _, want := range []string{"Show me", "use X", "Y is too slow", "## Decision", "## Rationale"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("show missing %q: %s", want, stdout)
		}
	}
}

func TestDecision_Confirm(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	id := seedDecision(t, tmp, "Confirm me")
	if _, _, err := runVorCmd(t, nil, "decision", "confirm", id[:8], "--repo", tmp); err != nil {
		t.Fatal(err)
	}
	out, _, _ := runVorCmd(t, nil, "decision", "show", id, "--repo", tmp)
	if !strings.Contains(out, "1.00 (exact)") {
		t.Errorf("confirm did not bump confidence/verification: %s", out)
	}
	if !strings.Contains(out, "status:       active") {
		t.Errorf("confirm did not flip status to active: %s", out)
	}
}

func TestDecision_Dismiss(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	id := seedDecision(t, tmp, "Dismiss me")
	if _, _, err := runVorCmd(t, nil, "decision", "dismiss", id[:8], "--repo", tmp); err != nil {
		t.Fatal(err)
	}
	out, _, _ := runVorCmd(t, nil, "decision", "show", id, "--repo", tmp)
	if !strings.Contains(out, "status:       deprecated") {
		t.Errorf("dismiss did not flip status: %s", out)
	}
	if !strings.Contains(out, "0.00") {
		t.Errorf("dismiss did not zero confidence: %s", out)
	}
}

func TestDecision_Deprecate(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	id := seedDecision(t, tmp, "Deprecate me")
	if _, _, err := runVorCmd(t, nil, "decision", "deprecate", id[:8], "--repo", tmp); err != nil {
		t.Fatal(err)
	}
	out, _, _ := runVorCmd(t, nil, "decision", "show", id, "--repo", tmp)
	if !strings.Contains(out, "status:       deprecated") {
		t.Errorf("deprecate did not flip status: %s", out)
	}
	// Confidence should NOT have been touched (deprecate keeps the
	// original confidence so the audit trail survives).
	if !strings.Contains(out, "0.80") {
		t.Errorf("deprecate should preserve confidence: %s", out)
	}
}

func TestDecision_HealthSummary(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"low-conf-1", "low-conf-2", "high-conf"} {
		if _, _, err := runVorCmd(t, nil, "decision", "add",
			"--title", title, "--confidence", "0.4", "--repo", tmp); err != nil {
			t.Fatal(err)
		}
	}
	out, _, err := runVorCmd(t, nil, "decision", "health", "--repo", tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"decisions:", "by source:", "by status:", "low confidence"} {
		if !strings.Contains(out, want) {
			t.Errorf("health output missing %q: %s", want, out)
		}
	}
}

func TestDecision_AmbiguousPrefix(t *testing.T) {
	tmp, _, conn := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	repoRow, _ := repos.New(conn).EnsureByLocalPath(context.Background(), tmp, "")
	store := decisionstore.New(conn)
	// Find a common 2-char prefix; UUIDs are random so this might be
	// flaky — we just insert several and look for a collision.
	ids := []string{}
	for i := 0; i < 32; i++ {
		id, _ := store.Insert(context.Background(), repoRow.ID, decisions.Record{
			Title: "ambiguous", Status: "active", Source: "cli", Confidence: 1.0,
		})
		ids = append(ids, id)
	}
	// Look for a 1-char prefix shared by ≥2 ids.
	byPrefix := map[byte][]string{}
	for _, id := range ids {
		byPrefix[id[0]] = append(byPrefix[id[0]], id)
	}
	var sharedPrefix byte
	for p, group := range byPrefix {
		if len(group) >= 2 {
			sharedPrefix = p
			break
		}
	}
	if sharedPrefix == 0 {
		t.Skip("no UUID collisions in 32 inserts — try again or assume the prefix-resolver works")
	}
	_, _, err := runVorCmd(t, nil, "decision", "confirm",
		string([]byte{sharedPrefix}), "--repo", tmp)
	if err == nil {
		t.Fatal("expected error for ambiguous prefix")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should mention 'ambiguous': %v", err)
	}
}
