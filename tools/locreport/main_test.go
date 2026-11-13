// carries: tools/locreport/*

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureRepo makes an empty git repository in a temporary directory and
// makes it the test's working directory (locreport reads the repository it
// runs in).
func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	t.Chdir(dir)
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes files (an empty content deletes the file) and commits the
// tree, returning the commit's id.
func commit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if content == "" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "fixture")
	return git(t, dir, "rev-parse", "HEAD")
}

func report(t *testing.T, args ...string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("run(%v) = %d, want 0\nstdout:\n%s\nstderr:\n%s", args, code, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

// row returns the table row of a package ("| name | ... |"), or "".
func row(table, name string) string {
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, "| "+name+" |") {
			return line
		}
	}
	return ""
}

const fixtureGo = `// Package a is a fixture.
package a

/* a block
comment */
const s = ` + "`" + `raw
// not a comment: inside a raw string

line` + "`" + `

func F() int { // trailing
	return 1
}
`

func TestReportTableGo(t *testing.T) {
	dir := fixtureRepo(t)
	base := commit(t, dir, map[string]string{
		"internal/polyforge/a/x.go":          fixtureGo,
		"internal/polyforge/a/x_test.go":     "package a\n\n// comment\nfunc TestX() {}\n",
		"internal/polyforge/a/testdata/f.go": "package f\n\nvar V = 1\n",
		"cmd/m/m.go":                         "package main\n\nfunc main() {\n}\n",
	})
	head := commit(t, dir, map[string]string{
		"internal/polyforge/a/x.go":      fixtureGo + "\nfunc G() {}\n",
		"internal/polyforge/a/x_test.go": "package a\n\n// comment\nfunc TestX() {}\n\nfunc TestY() {}\n",
		"cmd/m/m.go":                     "",
	})
	out, _ := report(t, base, head)
	for _, want := range []string{
		"`*_test.go`",
		"`testdata/`",
		"comments are removed",
		"blank lines",
		"`internal/<x>`",
		"| package | non-test base | non-test head | non-test Δ | test base | test head | test Δ |",
		"| internal/polyforge | 7 | 8 | +1 | 4 | 5 | +1 |",
		"| cmd | 3 | 0 | -3 | 0 | 0 | 0 |",
		"| total | 10 | 8 | -2 | 4 | 5 | +1 |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "| cmd |") > strings.Index(out, "| internal/polyforge |") || strings.Index(out, "| total |") < strings.Index(out, "| internal/polyforge |") {
		t.Errorf("rows are not sorted with the total last:\n%s", out)
	}
}

// The ticket's background table counted internal/polyforge's non-test Go
// code at 70,290 lines on 3bce1fbb (2026-11-11): the counting rule is pinned
// to that figure.
func TestInternalPolyforgeAt3bce1fbb(t *testing.T) {
	if err := exec.Command("git", "cat-file", "-e", "3bce1fbb^{commit}").Run(); err != nil {
		t.Skipf("this clone does not carry 3bce1fbb (a shallow clone or the public mirror): %v", err)
	}
	out, _ := report(t, "3bce1fbb", "3bce1fbb")
	got := row(out, "internal/polyforge")
	if !strings.HasPrefix(got, "| internal/polyforge | 70290 | 70290 | 0 |") {
		t.Fatalf("internal/polyforge row = %q, want non-test 70290 at 3bce1fbb\n%s", got, out)
	}
}

// fixturePy is the plan's docstring fixture: 11 code lines (17 were the
// docstrings code).
const fixturePy = `"""Module docstring."""

import os  # trailing comment

X = """not a docstring

# inside a string, counts
"""


class K:
    """Class docstring."""

    def f(self):
        """Function docstring.

        More text.
        """
        return "#" + r"\"#"


def g():
    '''One-line docstring.'''
    y = 1; """not a docstring: shares the logical line"""
    f"""f-string statement {y}"""
    return y
`

func TestPythonWorkflowScripts(t *testing.T) {
	t.Run("docstring", func(t *testing.T) {
		dir := fixtureRepo(t)
		base := commit(t, dir, nil)
		head := commit(t, dir, map[string]string{"workflows/_shared/scripts/a.py": fixturePy})
		out, _ := report(t, base, head)
		if want := "| workflows/_shared/scripts | 0 | 11 | +11 | 0 | 0 | 0 |"; !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	})
	t.Run("scope", func(t *testing.T) {
		dir := fixtureRepo(t)
		base := commit(t, dir, nil)
		head := commit(t, dir, map[string]string{
			"workflows/_shared/scripts/a.py":                 fixturePy,
			"workflows/_shared/scripts/pfstep/b.py":          "# comment\nx = \"#not a comment\"  # trailing\ny = r\"\\\"#\"\nz = 'it\\'s # not a comment'\n",
			"workflows/_shared/scripts/pfstep/gitmerge/c.py": "def h():\n    return '#'\n",
			"workflows/_shared/scripts/tests/test_x.py":      "\"\"\"Tests.\"\"\"\nimport unittest\n# c\n",
			"workflows/dev/scripts/d.py":                     "z = 1\n",
			"tools/other.py":                                 "a = 1\n",
			"workflows/_shared/notes.py":                     "b = 2\n",
		})
		out, _ := report(t, base, head)
		for _, want := range []string{
			"`workflows/**/scripts/**/*.py`",
			"`scripts/tests/`",
			"docstring",
			"| workflows/_shared/scripts | 0 | 16 | +16 | 0 | 1 | +1 |",
			"| workflows/dev/scripts | 0 | 1 | +1 | 0 | 0 | 0 |",
			"| total | 0 | 17 | +17 | 0 | 1 | +1 |",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("report lacks %q:\n%s", want, out)
			}
		}
		for _, absent := range []string{"| tools |", "| workflows |", "| workflows/_shared |"} {
			if strings.Contains(out, absent) {
				t.Errorf("report counts a file outside the scope (%q):\n%s", absent, out)
			}
		}
	})
}

// goLines is a Go file of n code lines.
func goLines(n int) string {
	var b strings.Builder
	b.WriteString("package a\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, "var v%d = 0\n", i)
	}
	return b.String()
}

func TestThresholdWarning(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		base, head map[string]string
		warn       string // a substring of the one ::warning line; "" = none
	}{
		{name: "above the default", base: nil, head: map[string]string{"a/a.go": goLines(1001)}, warn: "+1001"},
		{name: "at the default", base: nil, head: map[string]string{"a/a.go": goLines(1000)}},
		{name: "a net decrease above", args: []string{"-threshold", "5"}, base: map[string]string{"a/a.go": goLines(10)}, head: map[string]string{"a/a.go": goLines(4)}, warn: "-6"},
		{name: "test code only", base: nil, head: map[string]string{"a/a_test.go": goLines(2000)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := fixtureRepo(t)
			base := commit(t, dir, tc.base)
			head := commit(t, dir, tc.head)
			out, errOut := report(t, append(tc.args, base, head)...)
			if strings.Contains(out, "::warning") {
				t.Errorf("the table (the job summary) carries the annotation:\n%s", out)
			}
			if !strings.Contains(out, "| total |") {
				t.Errorf("no table:\n%s", out)
			}
			var warnings []string
			for _, line := range strings.Split(errOut, "\n") {
				if strings.HasPrefix(line, "::warning") {
					warnings = append(warnings, line)
				}
			}
			switch {
			case tc.warn == "" && len(warnings) != 0:
				t.Errorf("warned below the threshold: %q", warnings)
			case tc.warn != "" && (len(warnings) != 1 || !strings.Contains(warnings[0], tc.warn)):
				t.Errorf("want one ::warning naming %s, got %q (stderr %q)", tc.warn, warnings, errOut)
			case tc.warn != "" && len(tc.args) == 0 && !strings.Contains(warnings[0], "1000"):
				t.Errorf("the warning does not name the default threshold 1000: %q", warnings[0])
			}
		})
	}
}
