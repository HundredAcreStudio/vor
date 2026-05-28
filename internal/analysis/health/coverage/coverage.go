// Package coverage parses test-coverage reports (LCOV and Cobertura XML)
// into a normalised per-file form the rest of repowise can store and feed
// into the untested_hotspot biomarker.
//
// Only line coverage is required downstream; branch coverage is captured
// when the report provides it. File paths are returned as-is from the
// report — the caller normalises them to repo-relative POSIX paths.
package coverage

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Format identifies a coverage report dialect.
type Format string

const (
	FormatLCOV      Format = "lcov"
	FormatCobertura Format = "cobertura"
)

// FileCoverage is the normalised coverage for one source file.
type FileCoverage struct {
	Path           string
	Format         Format
	LinePct        float64  // 0..100
	BranchPct      *float64 // nil when the report has no branch data
	CoveredLines   []int    // line numbers with >0 hits, ascending
	TotalCoverable int      // instrumented (coverable) line count
}

// Detect guesses the report format from its content.
func Detect(data []byte) (Format, error) {
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("<?xml")) || bytes.Contains(trimmed, []byte("<coverage")) {
		return FormatCobertura, nil
	}
	if bytes.Contains(trimmed, []byte("\nSF:")) || bytes.HasPrefix(trimmed, []byte("SF:")) ||
		bytes.HasPrefix(trimmed, []byte("TN:")) {
		return FormatLCOV, nil
	}
	return "", fmt.Errorf("could not detect coverage format (expected LCOV or Cobertura XML)")
}

// Parse parses data in the given format. When format is empty it is
// auto-detected.
func Parse(data []byte, format Format) ([]FileCoverage, error) {
	if format == "" {
		f, err := Detect(data)
		if err != nil {
			return nil, err
		}
		format = f
	}
	switch format {
	case FormatLCOV:
		return parseLCOV(data)
	case FormatCobertura:
		return parseCobertura(data)
	default:
		return nil, fmt.Errorf("unsupported coverage format %q", format)
	}
}

// ---- LCOV ----------------------------------------------------------------

func parseLCOV(data []byte) ([]FileCoverage, error) {
	var out []FileCoverage
	var cur *FileCoverage
	var covered map[int]bool
	var brFound, brHit int

	flush := func() {
		if cur == nil {
			return
		}
		lines := make([]int, 0, len(covered))
		for ln, hit := range covered {
			if hit {
				lines = append(lines, ln)
			}
		}
		sort.Ints(lines)
		cur.CoveredLines = lines
		if cur.TotalCoverable > 0 {
			cur.LinePct = float64(len(lines)) / float64(cur.TotalCoverable) * 100
		}
		if brFound > 0 {
			pct := float64(brHit) / float64(brFound) * 100
			cur.BranchPct = &pct
		}
		out = append(out, *cur)
		cur, covered, brFound, brHit = nil, nil, 0, 0
	}

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "SF:"):
			flush()
			cur = &FileCoverage{Path: strings.TrimPrefix(line, "SF:"), Format: FormatLCOV}
			covered = map[int]bool{}
		case cur == nil:
			// Ignore preamble (TN: etc.) before the first SF:.
		case strings.HasPrefix(line, "DA:"):
			ln, hits, ok := parseDA(strings.TrimPrefix(line, "DA:"))
			if ok {
				if _, seen := covered[ln]; !seen {
					cur.TotalCoverable++
				}
				if hits > 0 {
					covered[ln] = true
				} else if _, seen := covered[ln]; !seen {
					covered[ln] = false
				}
			}
		case strings.HasPrefix(line, "BRF:"):
			brFound, _ = strconv.Atoi(strings.TrimPrefix(line, "BRF:"))
		case strings.HasPrefix(line, "BRH:"):
			brHit, _ = strconv.Atoi(strings.TrimPrefix(line, "BRH:"))
		case line == "end_of_record":
			flush()
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan lcov: %w", err)
	}
	flush() // trailing record with no end_of_record marker
	return out, nil
}

// parseDA parses an "<line>,<hits>[,checksum]" DA payload.
func parseDA(s string) (line, hits int, ok bool) {
	parts := strings.Split(s, ",")
	if len(parts) < 2 {
		return 0, 0, false
	}
	l, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return l, h, true
}

// ---- Cobertura -----------------------------------------------------------

type coberturaCoverage struct {
	XMLName  xml.Name           `xml:"coverage"`
	Packages []coberturaPackage `xml:"packages>package"`
	// Fallback for tools that omit the <packages> wrapper and nest
	// <classes> directly under <coverage>. Disjoint from the Packages
	// path above, so standard reports are not counted twice.
	Classes []coberturaClass `xml:"classes>class"`
}

type coberturaPackage struct {
	Classes []coberturaClass `xml:"classes>class"`
}

type coberturaClass struct {
	Filename   string          `xml:"filename,attr"`
	LineRate   string          `xml:"line-rate,attr"`
	BranchRate string          `xml:"branch-rate,attr"`
	Lines      []coberturaLine `xml:"lines>line"`
}

type coberturaLine struct {
	Number int `xml:"number,attr"`
	Hits   int `xml:"hits,attr"`
}

func parseCobertura(data []byte) ([]FileCoverage, error) {
	var doc coberturaCoverage
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse cobertura xml: %w", err)
	}
	// Merge classes that share a filename (partial classes / multiple
	// classes per file).
	byFile := map[string]*FileCoverage{}
	order := []string{}
	merge := func(c coberturaClass) {
		if c.Filename == "" {
			return
		}
		fc, ok := byFile[c.Filename]
		if !ok {
			fc = &FileCoverage{Path: c.Filename, Format: FormatCobertura}
			byFile[c.Filename] = fc
			order = append(order, c.Filename)
		}
		covered := map[int]bool{}
		for _, l := range fc.CoveredLines {
			covered[l] = true
		}
		for _, l := range c.Lines {
			fc.TotalCoverable++
			if l.Hits > 0 {
				covered[l.Number] = true
			}
		}
		lines := make([]int, 0, len(covered))
		for ln := range covered {
			lines = append(lines, ln)
		}
		sort.Ints(lines)
		fc.CoveredLines = lines
		if br := parseRate(c.BranchRate); br >= 0 {
			pct := br * 100
			fc.BranchPct = &pct
		}
	}
	for _, p := range doc.Packages {
		for _, c := range p.Classes {
			merge(c)
		}
	}
	for _, c := range doc.Classes {
		merge(c)
	}

	out := make([]FileCoverage, 0, len(order))
	for _, f := range order {
		fc := byFile[f]
		if fc.TotalCoverable > 0 {
			fc.LinePct = float64(len(fc.CoveredLines)) / float64(fc.TotalCoverable) * 100
		}
		out = append(out, *fc)
	}
	return out, nil
}

func parseRate(s string) float64 {
	if s == "" {
		return -1
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return -1
	}
	return v
}
