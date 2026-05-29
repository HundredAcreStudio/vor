package pipeline

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/settingsstore"
)

func TestIsDisableAction(t *testing.T) {
	for _, a := range []string{"disabled", "Disable", " off ", "skip", "ignore", "exclude", "none"} {
		if !isDisableAction(a) {
			t.Errorf("%q should be a disable action", a)
		}
	}
	for _, a := range []string{"warn", "enabled", "", "medium"} {
		if isDisableAction(a) {
			t.Errorf("%q should NOT be a disable action", a)
		}
	}
}

func TestHealthExcludesFor_MapsConfig(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	// Isolate LoadBootstrap from any real ~/.config/vor.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))

	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}

	const repoID = "repo1"
	healthRules := `[
		{"pattern":"**/*_test.go","overrides":{"high_complexity":"disabled","long_function":"warn"}},
		{"path":"generated/","overrides":{"*":"disabled"}}
	]`
	if err := settingsstore.New(conn).Set(ctx, repoID, config.KeyHealthRules, healthRules); err != nil {
		t.Fatal(err)
	}

	rules := healthExcludesFor(ctx, conn, repoID)
	if len(rules) != 2 {
		t.Fatalf("expected 2 exclude rules, got %d: %+v", len(rules), rules)
	}
	// First rule: only high_complexity disabled (long_function is "warn", not a
	// disable action, so it is not suppressed).
	if rules[0].Pattern != "**/*_test.go" || len(rules[0].Biomarkers) != 1 || rules[0].Biomarkers[0] != "high_complexity" {
		t.Errorf("rule 0 = %+v, want only high_complexity suppressed", rules[0])
	}
	if rules[1].Path != "generated/" || rules[1].Biomarkers[0] != "*" {
		t.Errorf("rule 1 = %+v, want path generated/ with *", rules[1])
	}
}
