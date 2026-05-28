package userconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// DaemonInfo records a running `vor serve` instance. Written
// on startup, removed on graceful shutdown. A missing file means
// "no daemon was started here, or the last one crashed".
type DaemonInfo struct {
	PID         int       `json:"pid"`
	Addr        string    `json:"addr"`
	StartedAt   time.Time `json:"started_at"`
	DatabaseURL string    `json:"database_url,omitempty"`
}

// SaveDaemon writes info to DaemonPath() atomically (write to .tmp,
// rename). Callers don't have to worry about partial files on crash.
func SaveDaemon(info *DaemonInfo) error {
	path, err := DaemonPath()
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadDaemon reads the daemon record. Returns (nil, nil) when missing.
func LoadDaemon() (*DaemonInfo, error) {
	path, err := DaemonPath()
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var info DaemonInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &info, nil
}

// ClearDaemon removes the record. No-op when already missing.
func ClearDaemon() error {
	path, err := DaemonPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Alive returns true when the PID in DaemonInfo is still running.
// Used by `vor status` to distinguish a current daemon record
// from a stale one (last daemon SIGKILLed before ClearDaemon ran).
//
// POSIX kill(pid, 0) is the canonical liveness probe — it errors with
// ESRCH if no such process exists. Linux/macOS both honour it; on
// Windows the signal-0 path is a no-op so we degrade to "assume alive"
// rather than reporting false negatives.
func (d *DaemonInfo) Alive() bool {
	if d == nil || d.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(d.PID)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}
