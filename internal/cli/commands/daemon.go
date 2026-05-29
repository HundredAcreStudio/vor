package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/userconfig"
)

// newDaemonCmd groups background-daemon lifecycle commands. `vor serve`
// runs the daemon in the foreground; these start/stop/inspect it in the
// background, tracked via the daemon.json record serve already writes.
func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start/stop/inspect the background serve daemon",
	}
	cmd.AddCommand(newDaemonStartCmd(), newDaemonStopCmd(), newDaemonStatusCmd(), newDaemonRestartCmd())
	return cmd
}

// daemonLogPath is where a backgrounded daemon's stdout/stderr go.
func daemonLogPath() (string, error) {
	dir, err := userconfig.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.log"), nil
}

func newDaemonStartCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Launch `vor serve` in the background",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			// Refuse to double-start a live daemon.
			if info, _ := userconfig.LoadDaemon(); info != nil && info.Alive() {
				fmt.Fprintf(out, "daemon already running (pid=%d, addr=%s)\n", info.PID, info.Addr)
				return nil
			}

			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locate vor binary: %w", err)
			}
			logPath, err := daemonLogPath()
			if err != nil {
				return err
			}
			logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return fmt.Errorf("open daemon log: %w", err)
			}
			defer logf.Close()

			serveArgs := []string{"serve"}
			if addr != "" {
				serveArgs = append(serveArgs, "--addr", addr)
			}
			child := exec.Command(exe, serveArgs...)
			child.Stdout = logf
			child.Stderr = logf
			child.Env = os.Environ()
			child.SysProcAttr = detachSysProcAttr()
			if err := child.Start(); err != nil {
				return fmt.Errorf("start daemon: %w", err)
			}
			pid := child.Process.Pid
			// Detach: we don't wait on the child; it owns its own lifetime.
			_ = child.Process.Release()

			// Wait for serve to record itself (it writes daemon.json with its
			// own pid on startup). Confirms it actually came up.
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if info, _ := userconfig.LoadDaemon(); info != nil && info.PID == pid && info.Alive() {
					fmt.Fprintf(out, "daemon started (pid=%d, addr=%s)\nlogs: %s\n", info.PID, info.Addr, logPath)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("daemon did not come up within 5s — check %s", logPath)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "host:port to bind (forwarded to `vor serve`)")
	return cmd
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background daemon (SIGTERM, graceful)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			info, _ := userconfig.LoadDaemon()
			if info == nil || info.PID == 0 {
				fmt.Fprintln(out, "no daemon recorded")
				return nil
			}
			if !info.Alive() {
				fmt.Fprintln(out, "daemon not running (clearing stale record)")
				_ = userconfig.ClearDaemon()
				return nil
			}

			proc, err := os.FindProcess(info.PID)
			if err != nil {
				return fmt.Errorf("find daemon process %d: %w", info.PID, err)
			}
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("signal daemon: %w", err)
			}

			// serve handles SIGTERM with a graceful shutdown that clears
			// daemon.json. Wait for it to exit.
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if cur, _ := userconfig.LoadDaemon(); cur == nil || !cur.Alive() {
					fmt.Fprintf(out, "daemon stopped (pid=%d)\n", info.PID)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("daemon (pid=%d) did not exit within 10s", info.PID)
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the background daemon is running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			info, _ := userconfig.LoadDaemon()
			if info == nil || info.PID == 0 {
				fmt.Fprintln(out, "daemon: not running")
				return nil
			}
			if !info.Alive() {
				fmt.Fprintf(out, "daemon: stale record (pid=%d gone) — run `vor daemon stop` to clear\n", info.PID)
				return nil
			}
			fmt.Fprintf(out, "daemon: running\n  pid:   %d\n  addr:  %s\n  since: %s\n",
				info.PID, info.Addr, info.StartedAt.Local().Format("2006-01-02 15:04:05"))
			if info.DatabaseURL != "" {
				fmt.Fprintf(out, "  db:    %s\n", info.DatabaseURL)
			}
			return nil
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Stop the daemon (if running) and start it again",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stop := newDaemonStopCmd()
			stop.SetOut(cmd.OutOrStdout())
			stop.SetErr(cmd.ErrOrStderr())
			if err := stop.RunE(stop, nil); err != nil {
				return err
			}
			start := newDaemonStartCmd()
			start.SetOut(cmd.OutOrStdout())
			start.SetErr(cmd.ErrOrStderr())
			if addr != "" {
				start.SetArgs([]string{"--addr", addr})
			}
			return start.Execute()
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "host:port to bind (forwarded to `vor serve`)")
	return cmd
}
