package cli

// w473_python_test.go: the step scripts' interpreter rule —
// [daemon] python, else python3 on PATH, at least MinPython — as the daemon
// applies it at its start and the doctor's python3 row reports it; the
// [daemon] python key's validation.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakePython writes an executable that answers version v the way the probe
// asks (any arguments).
func fakePython(t *testing.T, dir, name, v string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+v+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func lookIn(dir string) func(string) (string, error) {
	return func(name string) (string, error) {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			return "", errors.New("not found")
		}
		return p, nil
	}
}

func TestResolvePythonRule(t *testing.T) {
	dir := t.TempDir()
	onPath := fakePython(t, dir, "python3", "3.12.3")
	conf := fakePython(t, t.TempDir(), "py39", "3.9.6")
	old := fakePython(t, t.TempDir(), "py38", "3.8.10")
	junk := fakePython(t, t.TempDir(), "junk", "Python?")

	in, err := ResolvePython("", lookIn(dir), ProbePython)
	if err != nil || in.Path != onPath || in.Version != "3.12.3" || in.Source != PythonSourcePath {
		t.Fatalf("PATH python3: %+v %v", in, err)
	}
	in, err = ResolvePython(conf, lookIn(dir), ProbePython)
	if err != nil || in.Path != conf || in.Version != "3.9.6" || in.Source != PythonSourceConfig {
		t.Fatalf("the configured one wins over PATH, 3.9 is enough: %+v %v", in, err)
	}
	for _, c := range []struct {
		name, configured, path string
		want                   []string
	}{
		{"configured too old", old, dir, []string{"[daemon] python", old, "Python 3.8.10", "older than 3.9"}},
		{"configured missing (no fallback to PATH)", filepath.Join(dir, "nope"), dir, []string{"[daemon] python", "does not answer its version"}},
		{"configured answers junk", junk, dir, []string{"not a Python version"}},
		{"PATH has no python3", "", t.TempDir(), []string{"python3 is not on PATH"}},
	} {
		_, err := ResolvePython(c.configured, lookIn(c.path), ProbePython)
		if err == nil {
			t.Errorf("%s: resolved", c.name)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q lacks %q", c.name, err, w)
			}
		}
		if !strings.Contains(err.Error(), "pf config set python") {
			t.Errorf("%s: %q names no remedy", c.name, err)
		}
	}
	// the boundary, by version alone
	for v, ok := range map[string]bool{"3.9.0": true, "3.10.1": true, "4.0": true, "3.8.99": false, "2.7.18": false, "3": false, "3.x.1": false} {
		_, err := ResolvePython(fakePython(t, t.TempDir(), "p", v), nil, ProbePython)
		if (err == nil) != ok {
			t.Errorf("version %s: err %v, want ok=%v", v, err, ok)
		}
	}
}

// The doctor's python3 row: pass at >= 3.9, fail when missing or older.
func TestDoctorPythonRow(t *testing.T) {
	dir := t.TempDir()
	fakePython(t, dir, "python3", "3.12.3")
	if c := pythonCheck("", lookIn(dir), ProbePython); c.Name != "python3" || c.Status != "pass" || !strings.Contains(c.Detail, "Python 3.12.3") {
		t.Fatalf("a 3.12 on PATH: %+v", c)
	}
	if c := pythonCheck("", lookIn(t.TempDir()), ProbePython); c.Status != "fail" || !strings.Contains(c.Detail, "not on PATH") || !strings.Contains(c.Detail, "refuses to start") {
		t.Fatalf("no python3: %+v", c)
	}
	if c := pythonCheck(fakePython(t, t.TempDir(), "py", "3.8.10"), lookIn(dir), ProbePython); c.Status != "fail" || !strings.Contains(c.Detail, "older than 3.9") {
		t.Fatalf("a configured 3.8: %+v", c)
	}
	// this host's own python3, through the real lookup and probe (every
	// host that runs this bar carries Python >= 3.9: the doctor row is
	// what tells an operator otherwise)
	if c := pythonCheck("", exec.LookPath, ProbePython); c.Status != "pass" {
		t.Fatalf("this host's python3: %+v", c)
	}
}

// [daemon] python: a quoted absolute path; anything else names the line.
func TestDaemonPythonKey(t *testing.T) {
	s, err := parseDaemonTable("cli.toml", []daemonKV{{key: "python", val: `"/usr/bin/python3.12"`, line: 3}})
	if err != nil || s.Python != "/usr/bin/python3.12" {
		t.Fatalf("absolute: %+v %v", s, err)
	}
	for _, bad := range []string{`"python3"`, `"~/py/bin/python3"`, `/usr/bin/python3`} {
		_, err := parseDaemonTable("cli.toml", []daemonKV{{key: "python", val: bad, line: 3}})
		var ke *DaemonKeyError
		if !errors.As(err, &ke) || ke.Key != "python" || ke.Line != 3 {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if got := ConfigSchema("python"); got["kind"] != "path" {
		t.Errorf("schema %v", got)
	}
	if got := daemonValue(DaemonSettings{Python: "/p"}, "python"); got != "/p" {
		t.Errorf("daemonValue %q", got)
	}
}
