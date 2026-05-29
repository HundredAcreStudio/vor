package commands_test

import (
	"os"
	"path/filepath"
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

func TestDaemonLogs_NoLog(t *testing.T) {
	_, errOut, err := runVorCmd(t, isolatedState(t), "daemon", "logs")
	if err != nil {
		t.Fatalf("daemon logs: %v", err)
	}
	if !strings.Contains(errOut, "no daemon log yet") {
		t.Errorf("expected 'no daemon log yet', got: %s", errOut)
	}
}

func TestDaemonLogs_TailLines(t *testing.T) {
	env := isolatedState(t)
	logDir := filepath.Join(env["XDG_STATE_HOME"], "vor")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(filepath.Join(logDir, "daemon.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := runVorCmd(t, env, "daemon", "logs", "-n", "2")
	if err != nil {
		t.Fatalf("daemon logs -n 2: %v", err)
	}
	if out != "line4\nline5\n" {
		t.Errorf("expected last 2 lines, got: %q", out)
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
