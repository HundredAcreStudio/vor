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

	"github.com/HundredAcreStudio/vor/internal/analysis/decisions"
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

// Insert appends a single decision_records row plus a matching
// decision_evidence row. Used by `vor decision add` where the
// user creates a record by hand instead of mining one. Returns the
// generated id.
func (s *Store) Insert(ctx context.Context, repoID string, r decisions.Record) (string, error) {
	if repoID == "" {
		return "", fmt.Errorf("repoID is required")
	}
	if r.Title == "" {
		return "", fmt.Errorf("Title is required")
	}
	id := newID()
	altsJSON, _ := jsonEncode(r.Alternatives)
	consJSON, _ := jsonEncode(r.Consequences)
	filesJSON, _ := jsonEncode(r.AffectedFiles)
	tagsJSON, _ := jsonEncode(r.Tags)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	var evidenceFile interface{}
	if r.EvidenceFile != "" {
		evidenceFile = r.EvidenceFile
	}
	var evidenceLine interface{}
	if r.EvidenceLine > 0 {
		evidenceLine = r.EvidenceLine
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_records (
		id, repository_id, title, status, context, decision, rationale,
		alternatives_json, consequences_json, affected_files_json,
		affected_modules_json, tags_json, evidence_commits_json,
		source, evidence_file, evidence_line, confidence, verification
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, repoID, r.Title, defaultIfEmpty(r.Status, decisions.DefaultStatus),
		r.Context, r.Decision, r.Rationale,
		altsJSON, consJSON, filesJSON, "[]", tagsJSON, "[]",
		defaultIfEmpty(r.Source, "cli"),
		evidenceFile, evidenceLine,
		r.Confidence, defaultIfEmpty(r.Verification, decisions.VerificationExact),
	); err != nil {
		return "", fmt.Errorf("insert decision_records: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_evidence (
		id, decision_id, source, source_rank,
		evidence_file, evidence_line, evidence_commit, source_quote,
		confidence, verification
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), id, defaultIfEmpty(r.Source, "cli"), 1,
		evidenceFile, evidenceLine, r.EvidenceCommit, r.SourceQuote,
		r.Confidence, defaultIfEmpty(r.Verification, decisions.VerificationExact),
	); err != nil {
		return "", fmt.Errorf("insert decision_evidence: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// SetStatus moves a decision to the given status (active, deprecated,
// superseded, ...). Used by confirm / dismiss / deprecate.
func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	if id == "" || status == "" {
		return fmt.Errorf("id and status required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE decision_records SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, id)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no decision found with id %s", id)
	}
	return nil
}

// SetConfidence updates the confidence + verification fields. Used by
// confirm (1.0/exact) and dismiss (0/whatever).
func (s *Store) SetConfidence(ctx context.Context, id string, confidence float64, verification string) error {
	if id == "" {
		return fmt.Errorf("id required")
	}
	if verification == "" {
		verification = decisions.VerificationExact
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE decision_records SET confidence = ?, verification = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		confidence, verification, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no decision found with id %s", id)
	}
	return nil
}

// Get fetches one decision by id.
func (s *Store) Get(ctx context.Context, id string) (decisions.Record, string, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, status, COALESCE(context, ''), COALESCE(decision, ''),
		       COALESCE(rationale, ''), source,
		       COALESCE(evidence_file, ''), COALESCE(evidence_line, 0),
		       confidence, verification, affected_files_json, tags_json
		FROM decision_records WHERE id = ?
	`, id)
	var r decisions.Record
	var foundID, affectedJSON, tagsJSON string
	if err := row.Scan(&foundID, &r.Title, &r.Status, &r.Context, &r.Decision, &r.Rationale,
		&r.Source, &r.EvidenceFile, &r.EvidenceLine, &r.Confidence, &r.Verification,
		&affectedJSON, &tagsJSON); err != nil {
		return decisions.Record{}, "", err
	}
	if affectedJSON != "" && affectedJSON != "null" {
		_ = json.Unmarshal([]byte(affectedJSON), &r.AffectedFiles)
	}
	if tagsJSON != "" && tagsJSON != "null" {
		_ = json.Unmarshal([]byte(tagsJSON), &r.Tags)
	}
	return r, foundID, nil
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

// jsonUnmarshalLocal wraps encoding/json.Unmarshal so the caller can
// stay one indirection from the import.
func jsonUnmarshalLocal(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}
