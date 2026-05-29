//go:build unix

package commands

import "syscall"

// detachSysProcAttr starts the child in its own session (setsid) so it
// survives the parent CLI exiting and detaches from the controlling
// terminal — the basis of running `vor serve` in the background.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
