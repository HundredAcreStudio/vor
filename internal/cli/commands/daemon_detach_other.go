//go:build !unix

package commands

import "syscall"

// detachSysProcAttr is a no-op on non-unix platforms (no setsid). The child
// still runs, just without its own session.
func detachSysProcAttr() *syscall.SysProcAttr { return nil }
