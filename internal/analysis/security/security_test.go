package security_test

import (
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/security"
)

func kinds(fs []security.Finding) map[string]security.Finding {
	m := map[string]security.Finding{}
	for _, f := range fs {
		m[f.Kind] = f
	}
	return m
}

func TestScan_DetectsSecretsAndWeakCrypto(t *testing.T) {
	src := `import hashlib
API_KEY = "abcd1234efgh5678"
aws = "AKIAIOSFODNN7EXAMPLE"
h = hashlib.md5(data)
safe = compute(total)
`
	got := kinds(security.Scan("app.py", []byte(src)))
	for _, want := range []string{"hardcoded_secret", "aws_access_key", "weak_hash"} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected a %s finding", want)
		}
	}
	// The secret value must be redacted, never stored verbatim.
	if f := got["hardcoded_secret"]; strings.Contains(f.Snippet, "abcd1234efgh5678") {
		t.Errorf("secret value leaked into snippet: %q", f.Snippet)
	}
	if !strings.Contains(got["hardcoded_secret"].Snippet, "REDACTED") {
		t.Errorf("expected redaction marker, got %q", got["hardcoded_secret"].Snippet)
	}
}

func TestScan_PrivateKey(t *testing.T) {
	got := kinds(security.Scan("key.pem", []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIE...\n")))
	if f, ok := got["private_key"]; !ok || f.Severity != security.SeverityCritical {
		t.Errorf("expected critical private_key finding, got %+v", f)
	}
}

func TestScan_InjectionRequiresConcat(t *testing.T) {
	// Parameterised query — no concatenation, should NOT fire.
	clean := security.Scan("a.go", []byte(`rows, _ := db.Query("SELECT * FROM users WHERE id = ?", id)`))
	for _, f := range clean {
		if f.Kind == "sql_injection" {
			t.Errorf("parameterised query should not be flagged: %q", f.Snippet)
		}
	}
	// String-built query — should fire.
	dirty := security.Scan("b.go", []byte(`q := "SELECT * FROM users WHERE name = '" + name + "'"`))
	if _, ok := kinds(dirty)["sql_injection"]; !ok {
		t.Errorf("concatenated SQL should be flagged")
	}
}

func TestScan_SkipsDocFiles(t *testing.T) {
	// Markdown prose mentioning SQL keywords + backticks must not be
	// scanned at all (the README false-positive class).
	md := "Run `vor update` to refresh. The `SELECT` below is just docs:\n`SELECT * FROM x` + more text\n"
	if got := security.Scan("README.md", []byte(md)); len(got) != 0 {
		t.Errorf("doc file should yield no findings, got %d: %+v", len(got), got)
	}
	// Same content in a code file with concatenation does fire.
	code := `q := "SELECT * FROM x WHERE a='" + a + "'"`
	if got := kinds(security.Scan("q.go", []byte(code))); len(got) == 0 {
		t.Errorf("code file with built query should still fire")
	}
}

func TestScan_SqlRequiresQueryShape(t *testing.T) {
	// A bare "update" keyword next to a quote/concat is NOT a SQL injection.
	bare := `label := "update" + suffix`
	for _, f := range security.Scan("a.go", []byte(bare)) {
		if f.Kind == "sql_injection" {
			t.Errorf("bare keyword should not trip sql_injection: %q", f.Snippet)
		}
	}
	// A real UPDATE … SET built by concatenation does.
	real := `db.Exec("UPDATE users SET name='" + name + "' WHERE id=" + id)`
	if _, ok := kinds(security.Scan("b.go", []byte(real)))["sql_injection"]; !ok {
		t.Errorf("UPDATE ... SET concatenation should fire")
	}
}

func TestScan_CommandInjection(t *testing.T) {
	dirty := security.Scan("c.py", []byte(`os.system("rm -rf " + user_path)`))
	if _, ok := kinds(dirty)["command_injection"]; !ok {
		t.Errorf("os.system with concatenation should be flagged")
	}
	clean := security.Scan("d.py", []byte(`subprocess.run(["ls", "-l"])`))
	for _, f := range clean {
		if f.Kind == "command_injection" {
			t.Errorf("arg-list subprocess call should not fire: %q", f.Snippet)
		}
	}
}
