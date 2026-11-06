package cli

// w386_doctor_command_path_test.go: `pf doctor`'s "daemon
// command path" row shows the PATH a step runs under (GET /status
// step_path) and whether pf and the engine's own tools resolve under it —
// pf and git always, go when a mapped clone is a Go repo for the engine; a
// missing one is a warning naming the tool and the PATH.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w386ToolDir makes a directory holding executable stubs of tools.
func w386ToolDir(t *testing.T, tools ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range tools {
		if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// w386Workspace makes a workspace whose .repo holds one clone with files.
func w386Workspace(t *testing.T, files map[string]string) string {
	t.Helper()
	ws := t.TempDir()
	clone := filepath.Join(ws, ".repo", "app")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", clone).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	for rel, body := range files {
		p := filepath.Join(clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func TestW386DoctorCommandPath(t *testing.T) {
	goRepo := w386Workspace(t, map[string]string{"go.mod": "module x.test/app\n"})
	confOnly := w386Workspace(t, map[string]string{"go.mod": "module x.test/app\n", ".polyforge/verify-scope.conf": "stack=none\nfast_gate=make lint\n"})
	full := w386ToolDir(t, "pf", "git", "go")
	noGo := w386ToolDir(t, "pf", "git")
	path := func(dirs ...string) string { return strings.Join(dirs, string(os.PathListSeparator)) }
	run := func(present []string, commandPath string, up bool) (string, string) {
		r := &doctorRun{present: present, sockOK: up}
		r.status.StepPath = commandPath
		return commandPathCheck(r)
	}

	// a Go clone and go missing on the daemon's PATH: a warning naming go and the PATH
	p := path(noGo, "/nonexistent-w386")
	st, detail := run([]string{goRepo}, p, true)
	if st != "warn" || !strings.Contains(detail, "`go`") || !strings.Contains(detail, p) {
		t.Fatalf("missing go: want warn naming `go` and %q, got %s: %s", p, st, detail)
	}
	if strings.Contains(detail, "`git` does not") {
		t.Fatalf("git resolves and is not reported missing: %s", detail)
	}

	// everything resolves: no warning, and the PATH is shown
	p = path(full)
	st, detail = run([]string{goRepo}, p, true)
	if st != "pass" || !strings.Contains(detail, p) {
		t.Fatalf("complete: want pass showing %q, got %s: %s", p, st, detail)
	}

	// no clone the engine reads as Go (a stack=none conf pin): go is not needed
	st, detail = run([]string{confOnly}, path(noGo), true)
	if st != "pass" {
		t.Fatalf("no Go clone: go is not required, got %s: %s", st, detail)
	}

	// git missing is a warning naming it, whatever the clones
	p = path(t.TempDir())
	st, detail = run(nil, p, true)
	if st != "warn" || !strings.Contains(detail, "`git`") || !strings.Contains(detail, p) {
		t.Fatalf("missing git: want warn naming `git` and %q, got %s: %s", p, st, detail)
	}

	// pf missing (a step's `pf step …` would exit 127): a warning naming it
	p = path(w386ToolDir(t, "git"))
	st, detail = run(nil, p, true)
	if st != "warn" || !strings.Contains(detail, "`pf` does not resolve") || strings.Contains(detail, "`git` does not") {
		t.Fatalf("missing pf: want warn naming `pf` alone, got %s: %s", st, detail)
	}

	// the daemon down: its PATH is unknown, never faked
	if st, _ := run([]string{goRepo}, "", false); st != "skip" {
		t.Fatalf("daemon down: want skip, got %s", st)
	}
}
