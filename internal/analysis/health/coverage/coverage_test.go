package coverage_test

import (
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/health/coverage"
)

func find(cs []coverage.FileCoverage, path string) *coverage.FileCoverage {
	for i := range cs {
		if cs[i].Path == path {
			return &cs[i]
		}
	}
	return nil
}

func TestParseLCOV(t *testing.T) {
	in := `TN:
SF:src/calc.go
DA:1,5
DA:2,0
DA:3,2
BRF:4
BRH:2
LF:3
LH:2
end_of_record
SF:src/util.go
DA:1,1
DA:2,1
end_of_record
`
	cs, err := coverage.Parse([]byte(in), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cs) != 2 {
		t.Fatalf("want 2 files, got %d", len(cs))
	}
	calc := find(cs, "src/calc.go")
	if calc == nil {
		t.Fatal("calc.go missing")
	}
	if calc.Format != coverage.FormatLCOV {
		t.Errorf("format = %s", calc.Format)
	}
	if calc.TotalCoverable != 3 {
		t.Errorf("coverable = %d, want 3", calc.TotalCoverable)
	}
	// 2 of 3 lines covered -> 66.67%
	if calc.LinePct < 66.0 || calc.LinePct > 67.0 {
		t.Errorf("line pct = %.2f, want ~66.7", calc.LinePct)
	}
	if calc.BranchPct == nil || *calc.BranchPct != 50.0 {
		t.Errorf("branch pct = %v, want 50", calc.BranchPct)
	}
	if len(calc.CoveredLines) != 2 || calc.CoveredLines[0] != 1 || calc.CoveredLines[1] != 3 {
		t.Errorf("covered lines = %v, want [1 3]", calc.CoveredLines)
	}
	util := find(cs, "src/util.go")
	if util == nil || util.LinePct != 100.0 {
		t.Errorf("util.go should be 100%%, got %v", util)
	}
}

func TestParseCobertura(t *testing.T) {
	in := `<?xml version="1.0"?>
<coverage line-rate="0.5">
  <packages>
    <package name="app">
      <classes>
        <class filename="app/calc.py" line-rate="0.5" branch-rate="0.25">
          <lines>
            <line number="1" hits="3"/>
            <line number="2" hits="0"/>
          </lines>
        </class>
        <class filename="app/util.py" line-rate="1.0">
          <lines><line number="1" hits="1"/></lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	cs, err := coverage.Parse([]byte(in), "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cs) != 2 {
		t.Fatalf("want 2 files, got %d: %+v", len(cs), cs)
	}
	calc := find(cs, "app/calc.py")
	if calc == nil {
		t.Fatal("calc.py missing")
	}
	if calc.TotalCoverable != 2 {
		t.Errorf("coverable = %d, want 2 (no double counting)", calc.TotalCoverable)
	}
	if calc.LinePct != 50.0 {
		t.Errorf("line pct = %.2f, want 50", calc.LinePct)
	}
	if calc.BranchPct == nil || *calc.BranchPct != 25.0 {
		t.Errorf("branch pct = %v, want 25", calc.BranchPct)
	}
}

func TestDetect(t *testing.T) {
	if f, _ := coverage.Detect([]byte("SF:a.go\nend_of_record\n")); f != coverage.FormatLCOV {
		t.Errorf("expected lcov, got %s", f)
	}
	if f, _ := coverage.Detect([]byte(`<?xml version="1.0"?><coverage></coverage>`)); f != coverage.FormatCobertura {
		t.Errorf("expected cobertura, got %s", f)
	}
	if _, err := coverage.Detect([]byte("not a coverage file")); err == nil {
		t.Error("expected detection error")
	}
}
