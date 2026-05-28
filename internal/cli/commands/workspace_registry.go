package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/userconfig"
	"github.com/HundredAcreStudio/vor/internal/workspace"
)

// newWorkspaceRegisterCmd adds a workspace root to the user-global
// registry at $XDG_STATE_HOME/vor/workspaces.yaml. Different
// from `vor workspace add` (which adds a REPO to the current
// workspace.json): this one is the box-level inventory of workspaces.
func newWorkspaceRegisterCmd() *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "register [PATH]",
		Short: "Add a workspace root to the user-global registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) > 0 {
				target = args[0]
			}
			abs, err := filepath.Abs(target)
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(abs, ".vor", "workspace.json")); err != nil {
				return fmt.Errorf("%s is not a workspace root (no .vor/workspace.json) — run `vor workspace add` first to seed it", abs)
			}
			reg, err := userconfig.LoadWorkspaces()
			if err != nil {
				return err
			}
			if err := reg.Add(abs, label); err != nil {
				return err
			}
			if err := userconfig.SaveWorkspaces(reg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "registered %s\n", abs)
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "optional short name for this workspace")
	return cmd
}

func newWorkspaceUnregisterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unregister [PATH]",
		Short: "Remove a workspace root from the user-global registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) > 0 {
				target = args[0]
			}
			abs, err := filepath.Abs(target)
			if err != nil {
				return err
			}
			reg, err := userconfig.LoadWorkspaces()
			if err != nil {
				return err
			}
			if err := reg.Remove(abs); err != nil {
				return err
			}
			if err := userconfig.SaveWorkspaces(reg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "unregistered %s\n", abs)
			return nil
		},
	}
	return cmd
}

func newWorkspaceRegisteredCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registered",
		Short: "List every workspace registered with this user account",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := userconfig.LoadWorkspaces()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(reg.Workspaces) == 0 {
				fmt.Fprintln(out, "no workspaces registered — `vor workspace register PATH`")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  label\trepos\tpath\tsince")
			for _, e := range reg.Workspaces {
				count := "—"
				if state, err := workspace.Load(e.Path); err == nil {
					count = fmt.Sprintf("%d", len(state.Repos))
				}
				label := e.Label
				if label == "" {
					label = "—"
				}
				since := "—"
				if !e.RegisteredAt.IsZero() {
					since = e.RegisteredAt.Format("2006-01-02")
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", label, count, e.Path, since)
			}
			return nil
		},
	}
	return cmd
}
