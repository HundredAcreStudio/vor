package commands_test

import (
	"strings"
	"testing"
)

// isolatedState points XDG_STATE_HOME at a temp dir so daemon.json reads
// don't see (or touch) a real daemon.
func isolatedState(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"XDG_STATE_HOME": t.TempDir()}
}

func TestDaemonStatus_NotRunning(t *testing.T) {
	out, _, err := runVorCmd(t, isolatedState(t), "daemon", "status")
	if err != nil {
		t.Fatalf("daemon status: %v", err)
	}
	if !strings.Contains(out, "not running") {
		t.Errorf("expected 'not running', got: %s", out)
	}
}

func TestDaemonStop_NoDaemon(t *testing.T) {
	out, _, err := runVorCmd(t, isolatedState(t), "daemon", "stop")
	if err != nil {
		t.Fatalf("daemon stop: %v", err)
	}
	if !strings.Contains(out, "no daemon recorded") {
		t.Errorf("expected 'no daemon recorded', got: %s", out)
	}
}
