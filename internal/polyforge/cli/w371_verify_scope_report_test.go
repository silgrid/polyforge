package cli

// w371_verify_scope_report_test.go: — the scope engine's
// `report=<name>|junit|<path>` conf key: the named command's declared JUnit
// report rides on its --json command entry as `report: {format, path}`
// (null when undeclared, like `timeout`), binds to anonymous and named
// commands alike, and a malformed line is diagnosed and contributes
// nothing. Every case runs the in-tree engine through its documented
// surface in a git fixture.

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w371EngineReport is the subset of the --json report these tests read.
type w371EngineReport struct {
	Mode     string   `json:"mode"`
	Paths    []string `json:"paths"`
	Commands []struct {
		Name   string          `json:"name"`
		Phase  string          `json:"phase"`
		Cmd    string          `json:"cmd"`
		Report json.RawMessage `json:"report"`
	} `json:"commands"`
	Diagnostics []string `json:"diagnostics"`
}

// w371Engine answers the in-tree engine, skipping without its tools.
func w371Engine(t *testing.T) string {
	t.Helper()
	for _, bin := range []string{"git", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not installed: the engine fixtures need it", bin)
		}
	}
	engine, err := filepath.Abs(filepath.Join("..", "..", "..", "workflows", "_shared", "scripts", "verify-scope"))
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

// w371Scope runs the engine with --json in dir and decodes the report.
func w371Scope(t *testing.T, dir string, args ...string) w371EngineReport {
	t.Helper()
	cmd := exec.Command(w371Engine(t), append([]string{"--json"}, args...)...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("workflows/_shared/scripts/verify-scope --json %v: %v\nstderr: %s", args, err, stderr.String())
	}
	var r w371EngineReport
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("--json is not one object: %v\n%s", err, out)
	}
	return r
}

// w371Repo makes a git fixture with files committed as the base on main.
func w371Repo(t *testing.T, files map[string]string) string {
	t.Helper()
	w371Engine(t)
	dir := t.TempDir()
	w248Git(t, dir, "init", "-q", "-b", "main")
	for rel, body := range files {
		w248Write(t, dir, rel, body)
	}
	w248Git(t, dir, "add", "-A")
	w248Git(t, dir, "commit", "-q", "-m", "base")
	return dir
}

func TestW371VerifyScopeReportKey(t *testing.T) {
	conf := "stack=none\n" +
		"check=e2e|full|npm run test:e2e:all\n" +
		"full_command=go test ./...\n" +
		"check=lint|fast|npm run lint\n" +
		"report=e2e|junit|test-results/*.xml\n" +
		"report=full-1|junit|out/unit.xml\n" +
		// rejected: second for a name, unknown format, escaping or absolute
		// path, wrong shape, no such command
		"report=e2e|junit|other.xml\n" +
		"report=lint|tap|lint.tap\n" +
		"report=lint|junit|../up.xml\n" +
		"report=lint|junit|/abs.xml\n" +
		"report=lint\n" +
		"report=nope|junit|x.xml\n"
	dir := w371Repo(t, map[string]string{".polyforge/verify-scope.conf": conf, "a.txt": "a\n"})
	r := w371Scope(t, dir, "--base", "HEAD")
	got := map[string]string{}
	for _, c := range r.Commands {
		var d *struct{ Format, Path string }
		if err := json.Unmarshal(c.Report, &d); err != nil {
			t.Fatalf("command %s report %s: %v", c.Name, c.Report, err)
		}
		got[c.Name] = "null"
		if d != nil {
			got[c.Name] = d.Format + "|" + d.Path
		}
	}
	want := map[string]string{"lint": "null", "e2e": "junit|test-results/*.xml", "full-1": "junit|out/unit.xml"}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("command %s report = %s, want %s", name, got[name], w)
		}
	}
	for _, frag := range []string{
		"conf ignores a second report for 'e2e'",
		"conf rejects report format 'tap'",
		"conf rejects report path '../up.xml'",
		"conf rejects report path '/abs.xml'",
		"conf rejects report without the <name>|<format>|<path> shape",
		"conf ignores report for 'nope', which names no declared command",
	} {
		found := false
		for _, d := range r.Diagnostics {
			found = found || strings.Contains(d, frag)
		}
		if !found {
			t.Errorf("no diagnostic %q in %q", frag, r.Diagnostics)
		}
	}
}
