// carries: .polyforge/release.sh, test/w405_release_gate_test.go

package test

// w405_release_gate_test.go (polyforge#154): the release procedure runs NO
// test gate — the green integrate batch's own full-gate run is the test
// evidence, and the release's object is that batch's pinned commits
// (workflows/_shared/scripts/release.py resolves them, .polyforge/release.sh
// receives them with --pins). What is gone: the green-tag reuse, the tree's
// gate.py in its container, RELEASE_TEST_CMD. What stays: the xrefs
// doc-consistency gate, the dependency audit (polyforge#113's boundary) and
// the machine preflight — python3 included, for the pins file is parsed
// with it. The fake world still carries the gate's definition and tools
// (the real tree does, other tests read them): that a release never
// touches them is the pin.
//
// Run (targeted): GOWORK=off go test -count=1 -run '^TestW405' ./test/

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestW405ReleaseRunsNoTestGate: one plain release of the world's pinned
// head: no container is built, started or exec'd (no gate ran), the xrefs
// gate and the dependency audit still run, and the published release passes
// the script's own --verify.
func TestW405ReleaseRunsNoTestGate(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	out, err := w.runPinned(nil)
	if err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	calls := w.calls()
	for _, no := range []string{"docker gate-build", "docker run ", "docker exec ", "docker rm ", "tests"} {
		if relIndex(calls, no) >= 0 {
			t.Fatalf("the release must not run a test gate (%q in the calls): %v", no, calls)
		}
	}
	for _, want := range []string{"xrefs", "build", "audit"} {
		if relIndex(calls, want) < 0 {
			t.Fatalf("the release's own checks must still run (%q missing): %v", want, calls)
		}
	}
	if x, b, a := relIndex(calls, "xrefs"), relIndex(calls, "build"), relIndex(calls, "audit"); !(x < b && b < a) {
		t.Fatalf("the checks' order must be xrefs, build, audit: %v", calls)
	}
	if vout, verr := w.run(nil, "--verify", "v1.2.1"); verr != nil {
		t.Fatalf("verify: %v\n%s", verr, vout)
	}
}

// TestW405ReleaseScriptNamesNoGate: the script's executable lines name no
// test gate — no gate.py, no green tag, no make test, no RELEASE_TEST_CMD —
// and its usage declares the --pins convention the lane probes.
func TestW405ReleaseScriptNamesNoGate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", ".polyforge", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var code []string
	for _, ln := range strings.Split(string(raw), "\n") {
		if tl := strings.TrimSpace(ln); tl != "" && !strings.HasPrefix(tl, "#") {
			code = append(code, ln)
		}
	}
	src := strings.Join(code, "\n")
	for _, gone := range []string{"gate.py", "green-tag", "make test", "cmd/pf-gate", "RELEASE_TEST_CMD", "GATE_ID"} {
		if strings.Contains(src, gone) {
			t.Fatalf("release.sh still runs the test gate: %q is an executable line's text", gone)
		}
	}
	if !strings.Contains(src, "--pins") {
		t.Fatal("release.sh must receive the green batch's pins (--pins)")
	}
	usage := string(raw)
	if !strings.Contains(usage, "--pins <pins.json>") && !strings.Contains(usage, "--pins <file>") {
		t.Fatal("release.sh's usage must declare --pins (the lane probes --help for it)")
	}
}

// TestW405ReleasePreflightStillNeedsPython: the pins file is parsed with
// python3, so even a run whose xrefs and audit commands are both overridden
// needs it on the host — an old interpreter is a preflight problem naming
// the pins, before anything runs. With python3 3.8 the preflight refuses;
// nothing is built, published or claimed.
func TestW405ReleasePreflightStillNeedsPython(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	realPy, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	// a python3 that answers like 3.8 to the version questions and runs
	// everything else as the real one
	relWrite(t, filepath.Join(w.bin, "python3"), `#!/bin/sh
case "$*" in
  *"version_info < (3, 9)"*) exit 1 ;;
  *python_version*) echo 3.8.10; exit 0 ;;
esac
exec `+realPy+` "$@"
`, 0o755)
	out, err := w.runPinned([]string{"RELEASE_XREFS_CMD=echo xrefs >> \"$CALLLOG\"", "RELEASE_AUDIT_CMD=echo audit >> \"$CALLLOG\""})
	if err == nil {
		t.Fatalf("the release passed with python3 3.8:\n%s", out)
	}
	for _, want := range []string{"preflight failed", "python3 3.8.10 is older than 3.9", "pins", "no version claimed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if relIndex(w.calls(), "xrefs") >= 0 {
		t.Fatalf("a check ran on an old python3:\n%s", out)
	}
	if got := w.originTags(); strings.Contains(got, "v1.2.1") {
		t.Fatalf("tag pushed: %q", got)
	}
}
