package security_test

import (
	"strings"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/security"
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
