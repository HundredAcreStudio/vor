package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/repowise-dev/repowise-go/internal/providers"
)

// This file implements the three task-oriented (vs entity-oriented)
// tools that mirror the Python implementation's headline surface:
//
//   get_context — assemble a triage card per target (no LLM)
//   get_why     — synthesise the rationale behind code from decisions
//   get_answer  — synthesise a grounded answer to a NL question
//
// get_why and get_answer call the configured Provider. When none is
// configured they degrade to returning the retrieved raw material with
// a "synthesis unavailable" note, so the tools stay useful (and
// honest) on a provider-less server.

// ---- get_context ---------------------------------------------------------

type contextCard struct {
	Target      string   `json:"target"`
	Kind        string   `json:"kind,omitempty"`
	Name        string   `json:"name,omitempty"`
	Language    string   `json:"language,omitempty"`
	FilePath    string   `json:"file_path,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Hotspot     bool     `json:"hotspot"`
	CommitCount int      `json:"commit_count_90d,omitempty"`
	Decisions   []string `json:"decision_records,omitempty"`
	SymbolIDs   []string `json:"symbol_ids,omitempty"`
	Found       bool     `json:"found"`
}

func (s *Server) toolContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	targets := req.GetStringSlice("targets", nil)
	if len(targets) == 0 {
		// Accept a single `target` too for ergonomics.
		if one := req.GetString("target", ""); one != "" {
			targets = []string{one}
		}
	}
	if len(targets) == 0 {
		return mcp.NewToolResultError("provide `targets` (array) or `target` (string)"), nil
	}
	cards := make([]contextCard, 0, len(targets))
	for _, t := range targets {
		cards = append(cards, s.buildContextCard(ctx, rid, t))
	}
	return jsonResult(map[string]any{"cards": cards})
}

// buildContextCard assembles one triage card from the indexed tables.
// A "target" is either a file path or a symbol_id ("file.go::Sym").
// Pure data assembly — no LLM.
func (s *Server) buildContextCard(ctx context.Context, rid, target string) contextCard {
	card := contextCard{Target: target}

	// graph_nodes: match either node_id or file_path.
	row := s.opts.DB.QueryRowContext(ctx, `
		SELECT node_type, COALESCE(kind,''), COALESCE(name,''),
		       COALESCE(language,''), COALESCE(file_path, node_id)
		FROM graph_nodes
		WHERE repository_id = ? AND (node_id = ? OR file_path = ?)
		ORDER BY CASE node_type WHEN 'file' THEN 0 ELSE 1 END
		LIMIT 1`, rid, target, target)
	var nodeType string
	if err := row.Scan(&nodeType, &card.Kind, &card.Name, &card.Language, &card.FilePath); err == nil {
		card.Found = true
		if card.Kind == "" {
			card.Kind = nodeType
		}
	} else {
		// Target may be a path that isn't a graph node (e.g. a dir);
		// keep going — summary / decisions may still resolve.
		card.FilePath = target
	}

	// Page summary: file_overview keyed by path, symbol_detail keyed by id.
	pageKind := "file_overview"
	pageTarget := card.FilePath
	if nodeType != "file" && nodeType != "" {
		pageKind = "symbol_detail"
		pageTarget = target
	}
	_ = s.opts.DB.QueryRowContext(ctx, `
		SELECT summary FROM wiki_pages
		WHERE repository_id = ? AND page_type = ? AND target_path = ? LIMIT 1`,
		rid, pageKind, pageTarget).Scan(&card.Summary)

	// git_metadata hotspot signal (files only).
	var isHot sql.NullBool
	_ = s.opts.DB.QueryRowContext(ctx, `
		SELECT is_hotspot, commit_count_90d FROM git_metadata
		WHERE repository_id = ? AND file_path = ? LIMIT 1`,
		rid, card.FilePath).Scan(&isHot, &card.CommitCount)
	card.Hotspot = isHot.Valid && isHot.Bool

	// decision_records that name this path in affected_files_json or
	// evidence_file. LIKE on the JSON blob is coarse but adequate.
	drRows, err := s.opts.DB.QueryContext(ctx, `
		SELECT title FROM decision_records
		WHERE repository_id = ? AND (evidence_file = ? OR affected_files_json LIKE ?)
		ORDER BY confidence DESC LIMIT 10`,
		rid, card.FilePath, "%\""+card.FilePath+"\"%")
	if err == nil {
		for drRows.Next() {
			var title string
			if err := drRows.Scan(&title); err == nil {
				card.Decisions = append(card.Decisions, title)
			}
		}
		drRows.Close()
	}

	// Child symbol ids (when target is a file).
	if card.FilePath != "" {
		symRows, err := s.opts.DB.QueryContext(ctx, `
			SELECT node_id FROM graph_nodes
			WHERE repository_id = ? AND node_type != 'file' AND file_path = ?
			ORDER BY COALESCE(start_line, 0) LIMIT 50`,
			rid, card.FilePath)
		if err == nil {
			for symRows.Next() {
				var id string
				if err := symRows.Scan(&id); err == nil {
					card.SymbolIDs = append(card.SymbolIDs, id)
				}
			}
			symRows.Close()
		}
	}
	return card
}

// ---- retrieval (shared by get_answer) ------------------------------------

type retrievedDoc struct {
	Kind    string `json:"kind"` // "page" | "symbol"
	Path    string `json:"path"`
	Title   string `json:"title,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// retrieve pulls the most relevant indexed material for a free-text
// query. Tries FTS5 over page_fts first (rich, LLM-generated content),
// then a name LIKE over graph_nodes for symbol matches. Best-effort:
// a missing page_fts table (Postgres) just yields the graph matches.
func (s *Server) retrieve(ctx context.Context, rid, query string, limit int) []retrievedDoc {
	docs := []retrievedDoc{}

	if ftsQuery := sanitizeFTS(query); ftsQuery != "" {
		rows, err := s.opts.DB.QueryContext(ctx, `
			SELECT w.target_path, w.title, w.summary
			FROM page_fts f
			JOIN wiki_pages w ON w.id = f.page_id
			WHERE w.repository_id = ? AND page_fts MATCH ?
			LIMIT ?`, rid, ftsQuery, limit)
		if err == nil {
			for rows.Next() {
				var path, title, summary string
				if err := rows.Scan(&path, &title, &summary); err == nil {
					docs = append(docs, retrievedDoc{Kind: "page", Path: path, Title: title, Snippet: summary})
				}
			}
			rows.Close()
		}
	}

	// Symbol-name matches — one LIKE per significant term.
	for _, term := range significantTerms(query) {
		rows, err := s.opts.DB.QueryContext(ctx, `
			SELECT node_id, COALESCE(name,''), COALESCE(file_path, node_id)
			FROM graph_nodes
			WHERE repository_id = ? AND node_type != 'file' AND name LIKE ?
			ORDER BY pagerank DESC LIMIT 5`, rid, "%"+term+"%")
		if err != nil {
			continue
		}
		for rows.Next() {
			var id, name, path string
			if err := rows.Scan(&id, &name, &path); err == nil {
				docs = append(docs, retrievedDoc{Kind: "symbol", Path: id, Title: name, Snippet: "defined in " + path})
			}
		}
		rows.Close()
		if len(docs) >= limit {
			break
		}
	}
	return docs
}

// ---- get_answer ----------------------------------------------------------

func (s *Server) toolAnswer(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	question, err := req.RequireString("question")
	if err != nil {
		return mcp.NewToolResultError("`question` is required"), nil
	}
	docs := s.retrieve(ctx, rid, question, 12)

	// No provider → return the retrieval results as best-guesses so the
	// agent can follow up with get_context / get_symbol.
	if s.opts.Provider == nil {
		return jsonResult(map[string]any{
			"question":     question,
			"answer":       "",
			"confidence":   "none",
			"note":         "no LLM provider configured on this server — returning retrieved candidates only",
			"best_guesses": docs,
		})
	}
	if len(docs) == 0 {
		return jsonResult(map[string]any{
			"question":   question,
			"answer":     "I couldn't find indexed material relevant to that question.",
			"confidence": "low",
			"citations":  []retrievedDoc{},
		})
	}

	prompt := buildAnswerPrompt(question, docs)
	resp, err := s.opts.Provider.Generate(ctx, providers.Request{
		Model:     s.opts.Model,
		System:    answerSystemPrompt,
		Messages:  []providers.Message{{Role: providers.RoleUser, Content: prompt}},
		MaxTokens: 1024,
		Operation: "mcp_get_answer",
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("synthesis failed: %v", err)), nil
	}
	return jsonResult(map[string]any{
		"question":   question,
		"answer":     resp.Content,
		"confidence": confidenceFromDocs(docs),
		"citations":  docs,
	})
}

const answerSystemPrompt = `You answer questions about a codebase using ONLY the provided context snippets (generated documentation + symbol references). Ground every claim in the snippets. If the context is insufficient, say so plainly and name what additional file or symbol the asker should inspect. Never invent file paths, function names, or behaviour that isn't in the context. Keep the answer tight — a few sentences, with inline ` + "`code`" + ` references where useful.`

func buildAnswerPrompt(question string, docs []retrievedDoc) string {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(question)
	b.WriteString("\n\nContext snippets:\n")
	for i, d := range docs {
		fmt.Fprintf(&b, "\n[%d] (%s) %s\n", i+1, d.Kind, d.Path)
		if d.Title != "" {
			fmt.Fprintf(&b, "    title: %s\n", d.Title)
		}
		if d.Snippet != "" {
			fmt.Fprintf(&b, "    %s\n", d.Snippet)
		}
	}
	b.WriteString("\nAnswer the question grounded in the snippets above.")
	return b.String()
}

// ---- get_why -------------------------------------------------------------

type whyDecision struct {
	Title     string  `json:"title"`
	Decision  string  `json:"decision,omitempty"`
	Rationale string  `json:"rationale,omitempty"`
	Source    string  `json:"source"`
	Evidence  string  `json:"evidence,omitempty"`
	Conf      float64 `json:"confidence"`
}

func (s *Server) toolWhy(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rid, err := s.resolveRepoID(ctx, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	query := req.GetString("query", "")
	targets := req.GetStringSlice("targets", nil)
	if query == "" && len(targets) == 0 {
		return mcp.NewToolResultError("provide `query` and/or `targets`"), nil
	}

	decisions := s.matchDecisions(ctx, rid, query, targets, 12)
	if len(decisions) == 0 {
		return jsonResult(map[string]any{
			"query":     query,
			"rationale": "",
			"note":      "no architectural decisions matched — try `repowise_decisions` to browse all of them, or widen the query",
			"decisions": []whyDecision{},
		})
	}

	// No provider → return raw matched decisions.
	if s.opts.Provider == nil {
		return jsonResult(map[string]any{
			"query":     query,
			"rationale": "",
			"note":      "no LLM provider configured — returning matched decisions without synthesis",
			"decisions": decisions,
		})
	}

	prompt := buildWhyPrompt(query, targets, decisions)
	resp, err := s.opts.Provider.Generate(ctx, providers.Request{
		Model:     s.opts.Model,
		System:    whySystemPrompt,
		Messages:  []providers.Message{{Role: providers.RoleUser, Content: prompt}},
		MaxTokens: 768,
		Operation: "mcp_get_why",
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("synthesis failed: %v", err)), nil
	}
	return jsonResult(map[string]any{
		"query":     query,
		"rationale": resp.Content,
		"decisions": decisions, // raw evidence for verification
	})
}

const whySystemPrompt = `You explain WHY a codebase is shaped the way it is, using ONLY the architectural decision records provided. Synthesise the records into a short rationale. Attribute claims to their source (inline marker, ADR, CHANGELOG, commit). If the records don't actually explain the asked-about code, say so — don't speculate about motivations that aren't recorded.`

func buildWhyPrompt(query string, targets []string, decisions []whyDecision) string {
	var b strings.Builder
	if query != "" {
		fmt.Fprintf(&b, "Question: %s\n", query)
	}
	if len(targets) > 0 {
		fmt.Fprintf(&b, "About: %s\n", strings.Join(targets, ", "))
	}
	b.WriteString("\nDecision records:\n")
	for i, d := range decisions {
		fmt.Fprintf(&b, "\n[%d] %s (source: %s", i+1, d.Title, d.Source)
		if d.Evidence != "" {
			fmt.Fprintf(&b, ", %s", d.Evidence)
		}
		b.WriteString(")\n")
		if d.Decision != "" {
			fmt.Fprintf(&b, "    decision: %s\n", d.Decision)
		}
		if d.Rationale != "" {
			fmt.Fprintf(&b, "    rationale: %s\n", d.Rationale)
		}
	}
	b.WriteString("\nSynthesise the rationale grounded in these records.")
	return b.String()
}

// matchDecisions finds decision_records by free-text query (LIKE on
// title/decision/rationale) and/or by target path (affected files /
// evidence file). De-duplicated, confidence-ordered.
func (s *Server) matchDecisions(ctx context.Context, rid, query string, targets []string, limit int) []whyDecision {
	clauses := []string{}
	args := []any{rid}
	if query != "" {
		like := "%" + query + "%"
		clauses = append(clauses, "(title LIKE ? OR decision LIKE ? OR rationale LIKE ?)")
		args = append(args, like, like, like)
	}
	for _, t := range targets {
		clauses = append(clauses, "(evidence_file = ? OR affected_files_json LIKE ?)")
		args = append(args, t, "%\""+t+"\"%")
	}
	where := strings.Join(clauses, " OR ")
	q := `SELECT title, COALESCE(decision,''), COALESCE(rationale,''), source,
	             COALESCE(evidence_file,''), confidence
	      FROM decision_records WHERE repository_id = ? AND (` + where + `)
	      ORDER BY confidence DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.opts.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []whyDecision{}
	for rows.Next() {
		var d whyDecision
		if err := rows.Scan(&d.Title, &d.Decision, &d.Rationale, &d.Source, &d.Evidence, &d.Conf); err == nil {
			out = append(out, d)
		}
	}
	return out
}

// ---- helpers -------------------------------------------------------------

// sanitizeFTS turns a free-text query into an FTS5 MATCH expression:
// significant terms OR'd together, special chars stripped. Empty when
// nothing usable remains.
func sanitizeFTS(query string) string {
	terms := significantTerms(query)
	if len(terms) == 0 {
		return ""
	}
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + t + `"`
	}
	return strings.Join(quoted, " OR ")
}

// significantTerms splits a query into alphanumeric tokens ≥3 chars,
// dropping common stopwords so FTS/LIKE focus on the meaningful words.
func significantTerms(query string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		w := strings.ToLower(cur.String())
		cur.Reset()
		if len(w) < 3 || stopwords[w] {
			return
		}
		out = append(out, w)
	}
	for _, r := range query {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "how": true, "does": true,
	"why": true, "what": true, "where": true, "when": true, "are": true,
	"this": true, "that": true, "with": true, "from": true, "into": true,
	"use": true, "used": true, "using": true, "can": true, "you": true,
}

// confidenceFromDocs is a crude retrieval-quality signal: more + richer
// snippets → higher confidence. Maps to high/medium/low for the agent.
func confidenceFromDocs(docs []retrievedDoc) string {
	pages := 0
	for _, d := range docs {
		if d.Kind == "page" && d.Snippet != "" {
			pages++
		}
	}
	switch {
	case pages >= 3:
		return "high"
	case pages >= 1 || len(docs) >= 3:
		return "medium"
	default:
		return "low"
	}
}

// jsonUnmarshalShim keeps encoding/json referenced if future helpers
// need it; harmless no-op.
var _ = json.Marshal
