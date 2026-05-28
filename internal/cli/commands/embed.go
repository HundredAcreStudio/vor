package commands

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/repowise-dev/repowise-go/internal/config"
	"github.com/repowise-dev/repowise-go/internal/persistence/vector"
	"github.com/repowise-dev/repowise-go/internal/providers"

	// Register embedders so config.embedder can name one. Mock is the
	// zero-config default; real embedders register here as they land.
	_ "github.com/repowise-dev/repowise-go/internal/providers/mock"
)

// newEmbedCmd embeds generated wiki pages into the vector store so
// `repowise search --semantic` (and the MCP search tool) can do
// nearest-neighbour retrieval. Embedding is decoupled from page
// generation: `generate` produces the prose, `embed` indexes it.
//
// The embedder comes from config.embedder (default "mock" —
// deterministic, zero-cost, good for wiring + tests; a real embedder
// like OpenAI registers under its own name once implemented).
func newEmbedCmd() *cobra.Command {
	var (
		repoPath string
		repoID   string
		force    bool
	)
	cmd := &cobra.Command{
		Use:   "embed [PATH]",
		Short: "Embed generated wiki pages into the vector store for semantic search",
		Long: `Reads every wiki page for the repo, embeds its text with the
configured embedder (config.embedder, default "mock"), and upserts the
vector. Pages whose content hasn't changed since the last embed are
skipped unless --force is set.

Run after ` + "`repowise generate`" + `. Semantic search then works via
` + "`repowise search --semantic`" + ` and the MCP search tool.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root := repoPath
			if len(args) > 0 {
				root = args[0]
			}
			cfg, err := config.Load(root)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			conn, _, err := openDB(ctx, root)
			if err != nil {
				return err
			}
			defer conn.Close()
			repoRow, err := resolveReadRepo(ctx, conn, readRepoOptions{Path: root, ID: repoID})
			if err != nil {
				return err
			}

			embedder, err := buildEmbedder(cfg)
			if err != nil {
				return fmt.Errorf("build embedder %q: %w", cfg.Embedder, err)
			}

			out := cmd.OutOrStdout()
			summary, err := embedRepoPages(ctx, conn, repoRow.ID, embedder, force)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "embedded %d page(s), skipped %d unchanged (embedder=%s, dim=%d)\n",
				summary.embedded, summary.skipped, embedder.Name(), embedder.Dimensions())
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", ".", "repository path (overridden by positional PATH)")
	cmd.Flags().StringVar(&repoID, "repo-id", "", repoIDFlagDesc)
	cmd.Flags().BoolVar(&force, "force", false, "re-embed every page even when content is unchanged")
	return cmd
}

type embedSummary struct{ embedded, skipped int }

// embedRepoPages embeds each wiki page's (title + summary + content)
// into the vector store, skipping pages whose content hash matches a
// prior embed unless force is set. Exported logic kept here so the
// CLI command stays a thin shell.
func embedRepoPages(ctx context.Context, conn *sql.DB, repoID string, embedder providers.Embedder, force bool) (embedSummary, error) {
	vstore := vector.New(conn)
	prior := map[string]string{}
	if !force {
		prior, _ = vstore.ContentHashes(ctx, repoID, vector.KindPage)
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT target_path, title, COALESCE(summary,''), content
		FROM wiki_pages WHERE repository_id = ?`, repoID)
	if err != nil {
		return embedSummary{}, err
	}
	type pageText struct{ path, text, hash string }
	var pending []pageText
	var sum embedSummary
	for rows.Next() {
		var path, title, summary, content string
		if err := rows.Scan(&path, &title, &summary, &content); err != nil {
			rows.Close()
			return embedSummary{}, err
		}
		text := strings.TrimSpace(title + "\n" + summary + "\n" + content)
		hash := hashText(text)
		if !force && prior[path] == hash {
			sum.skipped++
			continue
		}
		pending = append(pending, pageText{path: path, text: text, hash: hash})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return embedSummary{}, err
	}
	if len(pending) == 0 {
		return sum, nil
	}

	// Batch the embed call — providers.Embedder.Embed takes many texts.
	texts := make([]string, len(pending))
	for i, p := range pending {
		texts[i] = p.text
	}
	vecs, err := embedder.Embed(ctx, texts)
	if err != nil {
		return embedSummary{}, fmt.Errorf("embed: %w", err)
	}
	if len(vecs) != len(pending) {
		return embedSummary{}, fmt.Errorf("embedder returned %d vectors for %d texts", len(vecs), len(pending))
	}
	for i, p := range pending {
		if err := vstore.Upsert(ctx, vector.Record{
			RepositoryID: repoID,
			TargetKind:   vector.KindPage,
			TargetPath:   p.path,
			Model:        embedder.Model(),
			ContentHash:  p.hash,
			Vector:       vecs[i],
		}); err != nil {
			return embedSummary{}, fmt.Errorf("store embedding for %s: %w", p.path, err)
		}
		sum.embedded++
	}
	return sum, nil
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// buildEmbedder constructs the configured embedder. Falls back to mock
// when unset so `embed` works zero-config.
func buildEmbedder(cfg config.Config) (providers.Embedder, error) {
	name := cfg.Embedder
	if name == "" {
		name = "mock"
	}
	opts := providers.Options{}
	if cfg.EmbeddingModel != "" {
		opts["model"] = cfg.EmbeddingModel
	}
	if cfg.EmbeddingDims > 0 {
		opts["dimensions"] = cfg.EmbeddingDims
	}
	return providers.NewEmbedder(name, opts)
}
