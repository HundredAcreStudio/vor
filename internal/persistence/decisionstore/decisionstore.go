// Package decisionstore persists decisions.Record values into the
// decision_records + decision_evidence + decision_node_links tables.
// ReplaceAll wipes the repo's existing snapshot and inserts the new
// set in one transaction, matching the pattern of every other store.
package decisionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/repowise-dev/repowise-go/internal/analysis/decisions"
)

// Store persists decision records.
type Store struct{ db *sql.DB }

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// ReplaceAll wipes decision_records (+ cascading evidence/links/edges)
// for repoID and inserts the supplied records. Each record yields one
// decision_records row, one decision_evidence row, and one
// decision_node_links row per AffectedFile.
func (s *Store) ReplaceAll(ctx context.Context, repoID string, records []decisions.Record) error {
	if repoID == "" {
		return fmt.Errorf("repoID is required")
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

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM decision_records WHERE repository_id = ?`, repoID); err != nil {
		return fmt.Errorf("delete decision_records: %w", err)
	}

	recStmt, err := tx.PrepareContext(ctx, `INSERT INTO decision_records (
		id, repository_id, title, status, context, decision, rationale,
		alternatives_json, consequences_json, affected_files_json,
		affected_modules_json, tags_json, evidence_commits_json,
		source, evidence_file, evidence_line, confidence, verification
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare record insert: %w", err)
	}
	defer recStmt.Close()

	evStmt, err := tx.PrepareContext(ctx, `INSERT INTO decision_evidence (
		id, decision_id, source, source_rank,
		evidence_file, evidence_line, evidence_commit, source_quote,
		confidence, verification
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare evidence insert: %w", err)
	}
	defer evStmt.Close()

	linkStmt, err := tx.PrepareContext(ctx, `INSERT INTO decision_node_links (
		id, repository_id, decision_id, node_id, link_type
	) VALUES (?, ?, ?, ?, 'file')`)
	if err != nil {
		return fmt.Errorf("prepare link insert: %w", err)
	}
	defer linkStmt.Close()

	for _, r := range records {
		decisionID := newID()

		altsJSON, _ := jsonEncode(r.Alternatives)
		consJSON, _ := jsonEncode(r.Consequences)
		filesJSON, _ := jsonEncode(r.AffectedFiles)
		tagsJSON, _ := jsonEncode(r.Tags)
		modulesJSON := "[]"
		commitsJSON := "[]"

		var evidenceLine interface{}
		if r.EvidenceLine > 0 {
			evidenceLine = r.EvidenceLine
		}
		var evidenceFile interface{}
		if r.EvidenceFile != "" {
			evidenceFile = r.EvidenceFile
		}

		if _, err := recStmt.ExecContext(ctx,
			decisionID, repoID, r.Title, defaultIfEmpty(r.Status, decisions.DefaultStatus),
			r.Context, r.Decision, r.Rationale,
			altsJSON, consJSON, filesJSON,
			modulesJSON, tagsJSON, commitsJSON,
			r.Source, evidenceFile, evidenceLine,
			r.Confidence, defaultIfEmpty(r.Verification, decisions.VerificationUnverified),
		); err != nil {
			return fmt.Errorf("insert decision_record %q: %w", r.Title, err)
		}

		// One canonical evidence row per record. (Multi-source decisions
		// — when ADR + git agree, for example — produce additional
		// evidence rows in later passes.)
		var commitCol interface{}
		if r.EvidenceCommit != "" {
			commitCol = r.EvidenceCommit
		}
		if _, err := evStmt.ExecContext(ctx,
			newID(), decisionID, r.Source, 1,
			evidenceFile, evidenceLine, commitCol, r.SourceQuote,
			r.Confidence, defaultIfEmpty(r.Verification, decisions.VerificationUnverified),
		); err != nil {
			return fmt.Errorf("insert decision_evidence: %w", err)
		}

		// One link row per affected file.
		for _, f := range r.AffectedFiles {
			if f == "" {
				continue
			}
			if _, err := linkStmt.ExecContext(ctx,
				newID(), repoID, decisionID, f,
			); err != nil {
				return fmt.Errorf("insert decision_node_links: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

// Count returns the number of decision_records rows for a repository.
func (s *Store) Count(ctx context.Context, repoID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM decision_records WHERE repository_id = ?`,
		repoID).Scan(&n)
	return n, err
}

// CountBySource tallies records per source identifier.
func (s *Store) CountBySource(ctx context.Context, repoID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source, COUNT(*) FROM decision_records
		 WHERE repository_id = ? GROUP BY source`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var src string
		var n int
		if err := rows.Scan(&src, &n); err != nil {
			return nil, err
		}
		out[src] = n
	}
	return out, rows.Err()
}

// jsonEncode marshals v as a JSON string, returning "[]"/"{}" defaults
// when v is nil or zero so the TEXT columns never end up holding
// "null".
func jsonEncode(v any) (string, error) {
	if v == nil {
		return "[]", nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "[]", err
	}
	s := string(data)
	if s == "null" {
		return "[]", nil
	}
	return s, nil
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func newID() string { return strings.ReplaceAll(uuid.New().String(), "-", "") }
