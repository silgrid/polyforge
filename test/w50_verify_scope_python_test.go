// carries: workflows/_shared/scripts/verify-scope

package test

// w50_verify_scope_python_test.go: the engine is a Python program
// (polyforge#50 AC1): workflows/_shared/scripts/verify-scope is rewritten in
// Python — same path, same executable bit, same command line and report —
// and the shell version is gone with no forwarding stub. The behaviour bar
// is the existing engine suite (AC2); this test pins the implementation
// language itself and the CLI contract's usage errors.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW50EngineIsPython$' -count=1

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w50EnginePath pins the shipped engine (the w25 helper's twin; kept separate
// so this file's red does not depend on that suite's fixtures).
func w50EnginePath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(w25e2eRoot(t), "workflows", "_shared", "scripts", "verify-scope")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("workflows/_shared/scripts/verify-scope must ship in the tree: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("workflows/_shared/scripts/verify-scope must be executable: mode %v", info.Mode())
	}
	return p
}

// TestW50EngineIsPython pins AC1's "Python 实现": the engine file opens with
// a python3 shebang and compiles as a Python program (a shell script does
// not), while the CLI contract's usage errors stay byte-for-byte (the
// expected messages are the shell engine's own, taken from its usage block).
func TestW50EngineIsPython(t *testing.T) {
	engine := w50EnginePath(t)
	raw, err := os.ReadFile(engine)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(raw), "\n", 2)
	if lines[0] != "#!/usr/bin/env python3" {
		t.Fatalf("the engine must be a Python program opening with `#!/usr/bin/env python3`, got %q", lines[0])
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed: the Python engine needs it")
	}
	if out, err := exec.Command("python3", "-c",
		"import sys; src = open(sys.argv[1], 'rb').read(); compile(src, sys.argv[1], 'exec')", engine).CombinedOutput(); err != nil {
		t.Fatalf("the engine must compile as a Python program: %v\n%s", err, out)
	}
	// The CLI contract: usage errors keep their exit codes and messages
	// (derived from the engine's usage block, not from any implementation).
	dir := t.TempDir()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--bogus"}, "verify-scope: unknown argument '--bogus'"},
		{[]string{"--light", "--full"}, "verify-scope: --light cannot be combined with --full"},
		{[]string{"--light", "--micro"}, "verify-scope: --light cannot be combined with --micro"},
		{[]string{"--base"}, "verify-scope: --base needs a revision"},
	} {
		cmd := exec.Command(engine, c.args...)
		cmd.Dir = dir
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("run %v: %v", c.args, err)
			}
			code = ee.ExitCode()
		}
		if code != 2 {
			t.Fatalf("%v: exit %d, want 2 (stderr %q)", c.args, code, errb.String())
		}
		if !strings.Contains(errb.String(), c.want) {
			t.Fatalf("%v: stderr %q must carry %q", c.args, errb.String(), c.want)
		}
	}
}

// TestW50EngineAcceptsTheReplayRuleKey pins the conf contract's reader split
// (polyforge#50 AC5): `not_runnable_rule=` is facts.py's key — the engine
// reads none of it, so it accepts the line without the unknown-key
// diagnostic (a conf may declare the key; no reader diagnoses a key
// another tool owns), like the workflow's other conf readers.
func TestW50EngineAcceptsTheReplayRuleKey(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	w25e2eGit(t, dir, "init", "-q", "-b", "main")
	w25e2eWrite(t, dir, ".polyforge/verify-scope.conf",
		"stack=none\nfast_gate=true\nnot_runnable_rule=demo|rule|^x \n")
	w25e2eWrite(t, dir, "a.txt", "a\n")
	base := w25e2eCommit(t, dir, "base")
	out, errb, code := w25e2eRun(t, dir, nil, "--json", "--base", base)
	if code != 0 {
		t.Fatalf("engine exited %d: %s", code, errb)
	}
	for _, diag := range strings.Split(errb, "\n") {
		if strings.Contains(diag, "not_runnable_rule") {
			t.Fatalf("the engine must not diagnose a conf key another tool owns: %s", diag)
		}
	}
	if !strings.Contains(out, `"fast_gate"`) && !strings.Contains(out, `"cmd": "true"`) {
		t.Fatalf("the conf's own commands still report: %s", out)
	}
}
