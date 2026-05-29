package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAddAugmentHook_Idempotent(t *testing.T) {
	s := map[string]any{}

	if !addAugmentHook(s, "vor-augment", false) {
		t.Fatal("first install should change settings")
	}
	if !hasAugmentGroup(postToolUseGroups(s)) {
		t.Fatal("augment group missing after install")
	}
	// Second install is a no-op.
	if addAugmentHook(s, "vor-augment", false) {
		t.Fatal("second install should be a no-op")
	}
	if got := len(postToolUseGroups(s)); got != 1 {
		t.Fatalf("expected exactly 1 group, got %d", got)
	}
}

func TestAddAugmentHook_ForceReplacesNotDuplicates(t *testing.T) {
	s := map[string]any{}
	addAugmentHook(s, "/old/path/vor-augment", false)
	if !addAugmentHook(s, "/new/path/vor-augment", true) {
		t.Fatal("force should rewrite")
	}
	groups := postToolUseGroups(s)
	if len(groups) != 1 {
		t.Fatalf("force should replace, not duplicate; got %d groups", len(groups))
	}
	cmd := groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"]
	if cmd != "/new/path/vor-augment" {
		t.Errorf("expected replaced command, got %v", cmd)
	}
}

func TestRemoveAugmentHook_PrunesScaffolding(t *testing.T) {
	s := map[string]any{}
	addAugmentHook(s, "vor-augment", false)
	if !removeAugmentHook(s) {
		t.Fatal("remove should change settings")
	}
	if _, ok := s["hooks"]; ok {
		t.Errorf("emptied hooks object should be pruned, got %v", s["hooks"])
	}
	// Removing again is a no-op.
	if removeAugmentHook(s) {
		t.Fatal("second remove should be a no-op")
	}
}

func TestRemoveAugmentHook_KeepsOtherGroups(t *testing.T) {
	other := map[string]any{
		"matcher": "Write",
		"hooks":   []any{map[string]any{"type": "command", "command": "other-tool"}},
	}
	s := map[string]any{"hooks": map[string]any{"PostToolUse": []any{other}}}
	addAugmentHook(s, "vor-augment", false)

	if !removeAugmentHook(s) {
		t.Fatal("remove should change settings")
	}
	groups := postToolUseGroups(s)
	if len(groups) != 1 {
		t.Fatalf("the unrelated group must survive; got %d groups", len(groups))
	}
	if hasAugmentGroup(groups) {
		t.Error("augment group should be gone")
	}
}

func TestApplyHookInstall_PreservesUnmanagedKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	seed := `{"model":"opus","permissions":{"allow":["Bash"]}}`
	if err := writeFile(path, []byte(seed)); err != nil {
		t.Fatal(err)
	}

	changed, err := applyHookInstall(path, "vor-augment", false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("install should change the file")
	}

	var got map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "opus" {
		t.Errorf("unmanaged key 'model' was lost: %v", got["model"])
	}
	if _, ok := got["permissions"]; !ok {
		t.Error("unmanaged key 'permissions' was lost")
	}
	if !hasAugmentGroup(postToolUseGroups(got)) {
		t.Error("hook not written")
	}

	// Round-trip: uninstall restores the original shape (no hooks key).
	if _, err := applyHookUninstall(path); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	var after map[string]any
	_ = json.Unmarshal(data, &after)
	if _, ok := after["hooks"]; ok {
		t.Errorf("uninstall should remove the hooks key, got %v", after["hooks"])
	}
	if after["model"] != "opus" {
		t.Error("uninstall clobbered an unmanaged key")
	}
}

func TestApplyHookUninstall_MissingFileNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.json")
	changed, err := applyHookUninstall(path)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if changed {
		t.Error("missing file should report no change")
	}
}

func TestResolveSettingsPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	p, scope, err := resolveSettingsPath(false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if scope != "global" || p != filepath.Join(home, ".claude", "settings.json") {
		t.Errorf("default should be global home settings, got scope=%s path=%s", scope, p)
	}

	p, scope, err = resolveSettingsPath(false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if scope != "project" || filepath.Base(p) != "settings.json" {
		t.Errorf("--project should target a local settings.json, got %s", p)
	}

	if _, _, err := resolveSettingsPath(true, true, nil); err == nil {
		t.Error("--global with --project should error")
	}
	if _, _, err := resolveSettingsPath(true, false, []string{"some/dir"}); err == nil {
		t.Error("--global with a project dir arg should error")
	}
}
