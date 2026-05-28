package health

import (
	"strings"

	gitignore "github.com/sabhiram/go-gitignore"
)

// ExcludeRule suppresses biomarker findings for files matching a glob
// (gitignore syntax, matched against the repo-relative path) or a path
// prefix. Biomarkers names the checks to suppress; empty or containing
// "*" suppresses every biomarker for the matched files.
//
// Rules are sourced from the config `health_rules` block, so the same
// pattern syntax used for ingestion ignores applies here too.
type ExcludeRule struct {
	Pattern    string
	Path       string
	Biomarkers []string
}

// compiledExclude is a prepared ExcludeRule: the glob compiled once, the
// biomarker filter as a set, and an "all" flag for whole-file exclusion.
type compiledExclude struct {
	gi     *gitignore.GitIgnore
	prefix string
	all    bool
	set    map[string]struct{}
}

// compileExcludes prepares rules for matching, dropping any that can match
// nothing (no pattern and no prefix).
func compileExcludes(rules []ExcludeRule) []compiledExclude {
	out := make([]compiledExclude, 0, len(rules))
	for _, r := range rules {
		c := compiledExclude{prefix: r.Path}
		if strings.TrimSpace(r.Pattern) != "" {
			c.gi = gitignore.CompileIgnoreLines(r.Pattern)
		}
		if c.gi == nil && c.prefix == "" {
			continue
		}
		if len(r.Biomarkers) == 0 {
			c.all = true
		} else {
			c.set = make(map[string]struct{}, len(r.Biomarkers))
			for _, b := range r.Biomarkers {
				if b == "*" {
					c.all = true
				}
				c.set[b] = struct{}{}
			}
		}
		out = append(out, c)
	}
	return out
}

func (c compiledExclude) matchesPath(path string) bool {
	if c.gi != nil && c.gi.MatchesPath(path) {
		return true
	}
	return c.prefix != "" && strings.HasPrefix(path, c.prefix)
}

// excluded reports whether a finding at (path, biomarker) is suppressed by
// any rule.
func excluded(rules []compiledExclude, path, biomarker string) bool {
	for _, c := range rules {
		if !c.matchesPath(path) {
			continue
		}
		if c.all {
			return true
		}
		if _, ok := c.set[biomarker]; ok {
			return true
		}
	}
	return false
}
