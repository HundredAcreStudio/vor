package commands

import (
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/HundredAcreStudio/vor/internal/userconfig"
)

// newWatchedCmd dumps the user-global watched.json registry — every
// repo `vor watch` has been run against, with the last-seen and
// last-update timestamps. Useful for "what am I keeping fresh on this
// box?" without grepping individual repo paths.
//
// Group placement: top-level rather than under `workspace` because
// the watched registry is a property of the user/box, not of any one
// workspace. Watched repos may live outside any registered workspace.
func newWatchedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watched",
		Short: "Inspect the user-global registry of watched repositories",
	}
	cmd.AddCommand(newWatchedListCmd())
	cmd.AddCommand(newWatchedClearCmd())
	return cmd
}

func newWatchedListCmd() *cobra.Command {
	var (
		stale      bool
		sortByPath bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every repo with a watch + update record on this box",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := userconfig.LoadWatched()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(reg.Repos) == 0 {
				fmt.Fprintln(out, "no watched repos yet — `vor watch [PATH]` records activity here")
				return nil
			}

			rows := append([]userconfig.WatchedRepo(nil), reg.Repos...)
			if sortByPath {
				sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
			} else {
				// Default order: most-recent watch first, so the
				// current focus shows up at the top.
				sort.Slice(rows, func(i, j int) bool {
					return rows[i].LastWatchedAt.After(rows[j].LastWatchedAt)
				})
			}

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  alias\tupdates\tlast update\tlast watched\tpath")
			shown := 0
			cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
			for _, w := range rows {
				if stale && w.LastWatchedAt.After(cutoff) {
					continue
				}
				alias := w.Alias
				if alias == "" {
					alias = "—"
				}
				lastUpdate := "—"
				if !w.LastUpdatedAt.IsZero() {
					lastUpdate = w.LastUpdatedAt.Format("2006-01-02 15:04")
				}
				fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\t%s\n",
					alias, w.UpdateCount, lastUpdate,
					w.LastWatchedAt.Format("2006-01-02 15:04"), w.Path)
				shown++
			}
			if shown == 0 {
				fmt.Fprintln(out, "no rows match the filter")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&stale, "stale", false, "only show repos with no watch activity in the last 7 days")
	cmd.Flags().BoolVar(&sortByPath, "sort-by-path", false, "sort alphabetically by path (default: most-recently-watched first)")
	return cmd
}

func newWatchedClearCmd() *cobra.Command {
	var confirm bool
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Reset the watched-repos registry to empty",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				fmt.Fprintln(cmd.OutOrStdout(),
					"This will wipe the watched-repos registry. Re-run with --yes to proceed.")
				return nil
			}
			if err := userconfig.SaveWatched(&userconfig.WatchedRegistry{}); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "watched registry cleared")
			return nil
		},
	}
	cmd.Flags().BoolVar(&confirm, "yes", false, "confirm the destructive clear")
	return cmd
}
