// Package graphstore persists *graph.Graph snapshots into the graph_nodes
// and graph_edges tables. Each call to ReplaceGraph wipes the previous
// snapshot for a repository and inserts the current one — fine for v1
// where ingestion runs as a whole-repo job. Incremental update is a
// follow-up.
package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// Store writes Graphs to the database.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// ReplaceGraph deletes the existing graph for repoID and writes g in its
// place. All work happens in a single transaction.
func (s *Store) ReplaceGraph(ctx context.Context, repoID string, g *graph.Graph) error {
	if repoID == "" {
		return fmt.Errorf("repoID is required")
	}
	if g == nil {
		return fmt.Errorf("nil graph")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_edges WHERE repository_id = ?`, repoID); err != nil {
		return fmt.Errorf("delete graph_edges: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_nodes WHERE repository_id = ?`, repoID); err != nil {
		return fmt.Errorf("delete graph_nodes: %w", err)
	}

	if err := insertNodes(ctx, tx, repoID, g.Nodes()); err != nil {
		return err
	}
	if err := insertEdges(ctx, tx, repoID, g.Edges()); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

func insertNodes(ctx context.Context, tx *sql.Tx, repoID string, nodes []*graph.Node) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO graph_nodes (
		id, repository_id, node_id, node_type, language,
		symbol_count, has_error, is_test, is_entry_point,
		pagerank, betweenness, community_id, community_meta_json,
		kind, name, qualified_name, file_path, start_line, end_line,
		visibility, signature, parent_symbol_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare node insert: %w", err)
	}
	defer stmt.Close()

	for _, n := range nodes {
		nodeType := "file"
		if n.Kind == graph.NodeSymbol {
			nodeType = "symbol"
		}
		var (
			kind, name, qname, filePath, vis, sig, parentID interface{}
			startLine, endLine                              interface{}
		)
		if n.Symbol != nil {
			kind = string(n.Symbol.Kind)
			name = n.Symbol.Name
			qname = n.Symbol.QualifiedName
			filePath = n.Path
			startLine = n.Symbol.StartLine
			endLine = n.Symbol.EndLine
			vis = string(n.Symbol.Visibility)
			sig = n.Symbol.Signature
			if n.Symbol.ParentName != nil {
				parentID = *n.Symbol.ParentName
			}
		}

		if _, err := stmt.ExecContext(ctx,
			newID(), repoID, n.StringID, nodeType, string(n.Language),
			n.SymbolCount, boolToInt(n.HasError), boolToInt(n.IsTest), boolToInt(n.IsEntryPoint),
			n.PageRank, n.Betweenness, n.CommunityID, "{}",
			kind, name, qname, filePath, startLine, endLine,
			vis, sig, parentID,
		); err != nil {
			return fmt.Errorf("insert graph_node %q: %w", n.StringID, err)
		}
	}
	return nil
}

func insertEdges(ctx context.Context, tx *sql.Tx, repoID string, edges []*graph.Edge) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO graph_edges (
		id, repository_id, source_node_id, target_node_id,
		imported_names_json, edge_type, confidence
	) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare edge insert: %w", err)
	}
	defer stmt.Close()

	for _, e := range edges {
		names := e.ImportedNames
		if names == nil {
			names = []string{}
		}
		raw, err := json.Marshal(names)
		if err != nil {
			return fmt.Errorf("marshal imported_names: %w", err)
		}
		if _, err := stmt.ExecContext(ctx,
			newID(), repoID, e.F.StringID, e.T.StringID,
			string(raw), string(e.Type), e.Confidence,
		); err != nil {
			return fmt.Errorf("insert graph_edge %s->%s (%s): %w", e.F.StringID, e.T.StringID, e.Type, err)
		}
	}
	return nil
}

// CountByEdgeType returns the per-type edge counts for a repository.
// Useful for `repowise status` style summaries.
func (s *Store) CountByEdgeType(ctx context.Context, repoID string) (map[models.EdgeType]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT edge_type, COUNT(*) FROM graph_edges WHERE repository_id = ? GROUP BY edge_type`,
		repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[models.EdgeType]int{}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[models.EdgeType(t)] = n
	}
	return out, rows.Err()
}

// CountNodes returns the number of graph_nodes rows for a repository.
func (s *Store) CountNodes(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_nodes WHERE repository_id = ?`, repoID).Scan(&n)
	return n, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func newID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")
}
