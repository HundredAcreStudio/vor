// Package embedtask registers semantic embedding generation as a post-pipeline
// task. It embeds the repo's wiki pages and stores the vectors so semantic
// search (vor_search / the MCP semantic path) can rank pages by cosine
// similarity — a feature that is otherwise dark because nothing else populates
// the embeddings table.
//
// It runs after wiki generation (higher Order) so there are pages to embed,
// and is incremental: a page whose embedded text is unchanged since the last
// run is skipped via the stored content hash.
//
// Import for side effects to register the task:
//
//	import _ "github.com/HundredAcreStudio/vor/internal/generation/embedtask"
package embedtask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	gmodels "github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/persistence/vector"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
	"github.com/HundredAcreStudio/vor/internal/pipeline/tasks"
)

// TaskID is the stable identifier under which embedding generation is
// registered and toggled.
const TaskID = "semantic_embeddings"

// maxEmbedChars caps the text sent to the embedder per page. Page bodies can be
// long; the title + summary + the head of the content carry the semantic signal
// and keep us comfortably under embedding-model input limits.
const maxEmbedChars = 6000

// embedBatch bounds how many pages are embedded per provider call.
const embedBatch = 64

// Task embeds wiki pages into the vector store for semantic search.
type Task struct{}

func (Task) ID() string   { return TaskID }
func (Task) Name() string { return "Semantic Embeddings" }
func (Task) Description() string {
	return "Embed wiki pages into the vector index so semantic search can rank by meaning. Requires an embedder; only changed pages are re-embedded."
}

// DefaultEnabled is true; the task self-skips when no real embedder is
// configured, so it stays zero-cost for repos without one.
func (Task) DefaultEnabled() bool { return true }

// Order runs after wiki generation (Order 10) so freshly-generated pages are
// available to embed in the same pass.
func (Task) Order() int { return 20 }

// Run embeds every wiki page whose text changed since the last run.
func (Task) Run(ctx context.Context, tc tasks.Context) (tasks.Result, error) {
	if tc.Embedder == nil {
		return tasks.Result{Skipped: true, Detail: "no embedder configured"}, nil
	}

	pages, err := wikistore.New(tc.DB).ListByRepo(ctx, tc.RepositoryID)
	if err != nil {
		return tasks.Result{}, fmt.Errorf("list pages: %w", err)
	}
	if len(pages) == 0 {
		return tasks.Result{Skipped: true, Detail: "no wiki pages to embed"}, nil
	}

	vstore := vector.New(tc.DB)
	model := tc.Embedder.Model()
	existing, err := vstore.ContentHashes(ctx, tc.RepositoryID, vector.KindPage)
	if err != nil {
		return tasks.Result{}, fmt.Errorf("load embedding hashes: %w", err)
	}

	// Collect the pages whose embedded text changed since last time.
	type pending struct {
		targetPath string
		text       string
		hash       string
	}
	var todo []pending
	skipped := 0
	for _, p := range pages {
		text := embedText(p)
		if text == "" {
			continue
		}
		h := contentHash(model, text)
		if existing[p.TargetPath] == h {
			skipped++
			continue
		}
		todo = append(todo, pending{targetPath: p.TargetPath, text: text, hash: h})
	}
	if len(todo) == 0 {
		return tasks.Result{Detail: fmt.Sprintf("0 embedded, %d up to date", skipped)}, nil
	}

	embedded := 0
	for start := 0; start < len(todo); start += embedBatch {
		if err := ctx.Err(); err != nil {
			return tasks.Result{}, err
		}
		end := min(start+embedBatch, len(todo))
		batch := todo[start:end]

		texts := make([]string, len(batch))
		for i, b := range batch {
			texts[i] = b.text
		}
		vecs, err := tc.Embedder.Embed(ctx, texts)
		if err != nil {
			return tasks.Result{}, fmt.Errorf("embed batch: %w", err)
		}
		if len(vecs) != len(batch) {
			return tasks.Result{}, fmt.Errorf("embedder returned %d vectors for %d inputs", len(vecs), len(batch))
		}
		for i, b := range batch {
			if err := vstore.Upsert(ctx, vector.Record{
				RepositoryID: tc.RepositoryID,
				TargetKind:   vector.KindPage,
				TargetPath:   b.targetPath,
				Model:        model,
				ContentHash:  b.hash,
				Vector:       vecs[i],
			}); err != nil {
				return tasks.Result{}, fmt.Errorf("store embedding for %s: %w", b.targetPath, err)
			}
			embedded++
		}
	}

	return tasks.Result{Detail: fmt.Sprintf("%d embedded, %d up to date", embedded, skipped)}, nil
}

// embedText builds the string embedded for a page: title, summary, then the
// head of the content. Truncated to maxEmbedChars.
func embedText(p gmodels.Page) string {
	var b strings.Builder
	if p.Title != "" {
		b.WriteString(p.Title)
		b.WriteString("\n\n")
	}
	if p.Summary != "" {
		b.WriteString(p.Summary)
		b.WriteString("\n\n")
	}
	b.WriteString(p.Content)
	text := strings.TrimSpace(b.String())
	if len(text) > maxEmbedChars {
		text = text[:maxEmbedChars]
	}
	return text
}

// contentHash keys an embedding by the embedded text and the model, so a model
// change re-embeds even when the text is identical.
func contentHash(model, text string) string {
	sum := sha256.Sum256([]byte(model + "\x00" + text))
	return hex.EncodeToString(sum[:])
}

func init() { tasks.Register(Task{}) }
