package userconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/userconfig"
)

// withXDGOverrides points $XDG_CONFIG_HOME and $XDG_STATE_HOME at a
// scratch dir so tests don't write into the user's real home.
func withXDGOverrides(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmp, "state"))
	return tmp
}

func TestConfigDir_RespectsXDG(t *testing.T) {
	tmp := withXDGOverrides(t)
	dir, err := userconfig.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, "config", "repowise")
	if dir != want {
		t.Errorf("ConfigDir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("ConfigDir not created: %v", err)
	}
}

func TestStateDir_RespectsXDG(t *testing.T) {
	tmp := withXDGOverrides(t)
	dir, err := userconfig.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, "state", "repowise")
	if dir != want {
		t.Errorf("StateDir = %q, want %q", dir, want)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	withXDGOverrides(t)
	c, err := userconfig.LoadConfig()
	if err != nil {
		t.Fatalf("expected no error on missing config: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil Config on missing file")
	}
	if c.Provider != "" {
		t.Errorf("expected zero-value Config, got %+v", c)
	}
}

func TestSaveLoadConfig_Roundtrip(t *testing.T) {
	withXDGOverrides(t)
	original := &userconfig.Config{
		Provider:    "anthropic",
		Model:       "claude-opus-4-7",
		DatabaseURL: "sqlite:/tmp/x.db",
		RPM:         600,
	}
	if err := userconfig.SaveConfig(original); err != nil {
		t.Fatal(err)
	}
	loaded, err := userconfig.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider != "anthropic" || loaded.Model != "claude-opus-4-7" {
		t.Errorf("roundtrip lost data: %+v", loaded)
	}
	if loaded.DatabaseURL != "sqlite:/tmp/x.db" {
		t.Errorf("DatabaseURL roundtrip = %q", loaded.DatabaseURL)
	}
	if loaded.RPM != 600 {
		t.Errorf("RPM roundtrip = %d", loaded.RPM)
	}
}

func TestSaveLoadDaemon_Roundtrip(t *testing.T) {
	withXDGOverrides(t)
	info := &userconfig.DaemonInfo{
		PID:           os.Getpid(),
		Addr:          "127.0.0.1:7337",
		WorkspaceRoot: "/home/user/work",
	}
	if err := userconfig.SaveDaemon(info); err != nil {
		t.Fatal(err)
	}
	loaded, err := userconfig.LoadDaemon()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Addr != info.Addr || loaded.PID != info.PID {
		t.Errorf("daemon roundtrip mismatch: %+v vs %+v", loaded, info)
	}
}

func TestLoadDaemon_MissingFile(t *testing.T) {
	withXDGOverrides(t)
	d, err := userconfig.LoadDaemon()
	if err != nil {
		t.Fatalf("missing daemon.json should not error: %v", err)
	}
	if d != nil {
		t.Errorf("expected nil for missing daemon, got %+v", d)
	}
}

func TestClearDaemon(t *testing.T) {
	withXDGOverrides(t)
	_ = userconfig.SaveDaemon(&userconfig.DaemonInfo{PID: 1234, Addr: ":7337"})
	if err := userconfig.ClearDaemon(); err != nil {
		t.Fatal(err)
	}
	if d, _ := userconfig.LoadDaemon(); d != nil {
		t.Errorf("expected nil after Clear, got %+v", d)
	}
	// Clearing twice is OK.
	if err := userconfig.ClearDaemon(); err != nil {
		t.Errorf("second Clear errored: %v", err)
	}
}

func TestDaemonAlive_RealProcess(t *testing.T) {
	info := &userconfig.DaemonInfo{PID: os.Getpid()}
	if !info.Alive() {
		t.Error("self PID should be alive")
	}
}

func TestDaemonAlive_NilAndZero(t *testing.T) {
	var nilInfo *userconfig.DaemonInfo
	if nilInfo.Alive() {
		t.Error("nil DaemonInfo should not report alive")
	}
	if (&userconfig.DaemonInfo{PID: 0}).Alive() {
		t.Error("PID=0 should not report alive")
	}
}

func TestDaemonAlive_DeadPID(t *testing.T) {
	// Pick a PID that almost certainly isn't running. We can't be
	// 100% sure, but values near uint32 max are practically never
	// allocated on a normal system.
	info := &userconfig.DaemonInfo{PID: 0x7FFFFFFE}
	if info.Alive() {
		t.Skip("unlikely-PID was actually alive; skipping flake")
	}
}
