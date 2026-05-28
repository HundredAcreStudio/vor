package commands

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/persistence/coststore"
)

// newCostsCmd prints LLM spend from llm_costs.
func newCostsCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
	)
	cmd := &cobra.Command{
		Use:   "costs",
		Short: "Show LLM spend recorded for this repository",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			conn, _, err := openDB(ctx, repoPath)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: repoPath, ID: repoID})
			if err != nil {
				return err
			}
			store := coststore.New(conn)

			total, err := store.TotalUSD(ctx, repoRow.ID)
			if err != nil {
				return fmt.Errorf("TotalUSD: %w", err)
			}
			byOp, err := store.TotalByOperation(ctx, repoRow.ID)
			if err != nil {
				return fmt.Errorf("TotalByOperation: %w", err)
			}
			count, err := store.Count(ctx, repoRow.ID)
			if err != nil {
				return fmt.Errorf("Count: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "LLM spend: $%.4f across %d calls\n", total, count)
			if len(byOp) == 0 {
				return nil
			}
			ops := make([]string, 0, len(byOp))
			for k := range byOp {
				ops = append(ops, k)
			}
			sort.Strings(ops)
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			defer tw.Flush()
			fmt.Fprintln(tw, "  operation\tspend")
			for _, op := range ops {
				fmt.Fprintf(tw, "  %s\t$%.4f\n", op, byOp[op])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	return cmd
}
