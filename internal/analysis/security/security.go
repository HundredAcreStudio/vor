// Package security is a lightweight, pattern-based vulnerability scanner.
// It is deliberately deterministic and dependency-free: each rule is a
// regular expression (optionally requiring a concatenation/interpolation
// indicator on the same line) applied line-by-line to source files.
//
// This is a linter-grade heuristic, not a dataflow analysis — it surfaces
// candidates for human review (hardcoded secrets, weak crypto, likely
// injection sinks), so findings favour recall and are clearly attributed
// to their rule. Secret values are redacted before they are stored.
package security

import (
	"bufio"
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
)

// Severity levels for findings (mirror the health package's vocabulary).
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
)

// Finding is one rule hit at a specific line.
type Finding struct {
	FilePath string
	Kind     string // rule id, stored as security_findings.kind
	Severity string
	Snippet  string // redacted, single line, truncated
	Line     int    // 1-indexed
}

// rule is one detection pattern.
type rule struct {
	kind     string
	severity string
	re       *regexp.Regexp
	// requireConcat: only fire when the line also shows string building
	// (concatenation / interpolation), which is what turns a benign sink
	// into an injection risk.
	requireConcat bool
	// redact: replace the matched substring with a placeholder so secrets
	// are never persisted verbatim.
	redact bool
}

// rules is the curated detection set. Patterns aim for high signal; the
// requireConcat rules avoid flagging every parameterised query / exec.
var rules = []rule{
	{
		kind: "private_key", severity: SeverityCritical, redact: true,
		re: regexp.MustCompile(`-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`),
	},
	{
		kind: "aws_access_key", severity: SeverityHigh, redact: true,
		re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	},
	{
		kind: "hardcoded_secret", severity: SeverityHigh, redact: true,
		re: regexp.MustCompile(`(?i)(?:api[_-]?key|secret[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret|password|passwd)\s*[:=]\s*["'][^"'\s]{8,}["']`),
	},
	{
		kind: "weak_hash", severity: SeverityMedium,
		re: regexp.MustCompile(`(?i)hashlib\.(?:md5|sha1)\s*\(|MessageDigest\.getInstance\(\s*["'](?:MD5|SHA-?1)|crypto/(?:md5|sha1)|CryptoJS\.(?:MD5|SHA1)\b`),
	},
	{
		kind: "weak_cipher", severity: SeverityMedium,
		re: regexp.MustCompile(`(?i)Cipher\.getInstance\(\s*["'](?:DES|RC4|[A-Z0-9/]*ECB)|\bcrypto/des\b`),
	},
	{
		// Require real query shape (SELECT … FROM, INSERT INTO <t>, UPDATE
		// <t> SET, DELETE FROM <t>) rather than a bare keyword, so prose
		// like a Markdown `update` doesn't match. Paired with requireConcat
		// below, this targets string-built queries.
		kind: "sql_injection", severity: SeverityHigh, requireConcat: true,
		re: regexp.MustCompile(`(?i)\b(?:SELECT\s+[\w*]|INSERT\s+INTO\s+\w|UPDATE\s+\w+\s+SET\b|DELETE\s+FROM\s+\w)`),
	},
	{
		kind: "command_injection", severity: SeverityHigh, requireConcat: true,
		re: regexp.MustCompile(`(?i)os\.system|subprocess\.(?:call|run|Popen)|exec\.Command|Runtime\.getRuntime\(\)\.exec|child_process\.exec(?:Sync)?`),
	},
}

// concatIndicator marks lines that build strings dynamically — the trigger
// for the requireConcat rules.
var concatIndicator = regexp.MustCompile(`\+\s*\w|\$\{|%s|%d|\bformat\b|f["']|\.format\(|` + "`")

const maxSnippet = 160

// docExtensions are prose/documentation files. The rules target source
// and config, not prose — a Markdown `update` or a quoted SQL keyword in
// docs is not a vulnerability — so these are skipped entirely.
var docExtensions = map[string]struct{}{
	".md": {}, ".markdown": {}, ".mdx": {}, ".txt": {},
	".rst": {}, ".adoc": {}, ".org": {},
}

// scannable reports whether path is a file the scanner should inspect.
func scannable(path string) bool {
	_, isDoc := docExtensions[strings.ToLower(filepath.Ext(path))]
	return !isDoc
}

// Scan applies every rule to source and returns findings for path. Prose/
// documentation files are skipped. Lines over a generous length cap are
// still scanned but snippets are truncated.
func Scan(path string, source []byte) []Finding {
	if !scannable(path) {
		return nil
	}
	var out []Finding
	sc := bufio.NewScanner(bytes.NewReader(source))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, r := range rules {
			loc := r.re.FindStringIndex(line)
			if loc == nil {
				continue
			}
			if r.requireConcat && !concatIndicator.MatchString(line) {
				continue
			}
			out = append(out, Finding{
				FilePath: path,
				Kind:     r.kind,
				Severity: r.severity,
				Snippet:  snippet(line, loc, r.redact),
				Line:     lineNo,
			})
		}
	}
	return out
}

// snippet returns a trimmed, length-capped line. When redact is set the
// matched span is replaced so secret material is never persisted.
func snippet(line string, loc []int, redact bool) string {
	s := line
	if redact && loc != nil && loc[0] >= 0 && loc[1] <= len(line) {
		s = line[:loc[0]] + "***REDACTED***" + line[loc[1]:]
	}
	s = strings.TrimSpace(s)
	if len(s) > maxSnippet {
		s = s[:maxSnippet] + "…"
	}
	return s
}
