package verifyrun

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestGoRerunCarriesThePlannedFlags: a targeted Go re-run keeps the
// planned command's assignments and flags (a -race or -tags dropped would
// turn a real failure into a flaky pass) and sets only its own.
func TestGoRerunCarriesThePlannedFlags(t *testing.T) {
	cases := []struct{ cmd, run, target, want, why string }{
		{"GOWORK=off go test -race -tags 'a b' -count=3 -v -run Foo -json ./...", "^TestX$", "./a/",
			"GOWORK=off go test -race -tags 'a b' -count=1 -v -timeout 20m0s -run '^TestX$' ./a/", ""},
		{"go test -shuffle on -timeout=5m ./pkg/...", "", "example.com/m/pkg",
			"go test -shuffle on -timeout=5m -count=1 -v example.com/m/pkg", ""}, // the plan's timeout stays: a hang must not pass
		{"GOWORK=off make test", "^TestY$", "./test/",
			"GOWORK=off go test -count=1 -v -timeout 20m0s -run '^TestY$' ./test/", ""},
		{"go test ./... -args -flag", "^TestZ$", ".", "", "-args"},
	}
	for _, c := range cases {
		got, why := goRerun(c.cmd, c.run, c.target, 20*time.Minute)
		if got != c.want || (c.why != "" && !strings.Contains(why, c.why)) {
			t.Errorf("goRerun(%q) = %q, %q; want %q (%q)", c.cmd, got, why, c.want, c.why)
		}
	}
}

// TestPyRerunCarriesOptionsAndQuotesAssignments: a pytest/unittest
// re-run keeps the environment assignments and options raw as written
// (so they stay assignments and mean what they meant), and refuses an
// option whose value it cannot tell from a path.
func TestPyRerunCarriesOptionsAndQuotesAssignments(t *testing.T) {
	got, why := pyRerun(`PYTEST_ADDOPTS="-p no:cacheprovider" python3 -m pytest -q --strict-markers tests`, "pytest", "tests/a.py::test_x", pytestSwitches)
	if got != `PYTEST_ADDOPTS="-p no:cacheprovider" python3 -m pytest -q --strict-markers tests/a.py::test_x` || why != "" {
		t.Errorf("pytest re-run = %q (%q)", got, why)
	}
	if got, why := pyRerun("pytest -W error tests", "pytest", "tests/a.py::test_x", pytestSwitches); got != "" || !strings.Contains(why, "-W") {
		t.Errorf("an option with a separate value must refuse the re-run: %q %q", got, why)
	}
	if got, _ := pyRerun("python3 -m unittest -v pkg.test_m", "unittest", "pkg.test_m.T.test_x", unittestSwitches); got != "python3 -m unittest -v pkg.test_m.T.test_x" {
		t.Errorf("unittest re-run = %q", got)
	}
	if got, why := pyRerun("python3 -m unittest discover -s tests", "unittest", "a.T.test_x", unittestSwitches); got != "" || !strings.Contains(why, "discover") {
		t.Errorf("discover must refuse: %q %q", got, why)
	}
}

// TestRaceFailureIsNeverFlaky: a run that reported a data race is never
// proven flaky by a re-run without -race.
func TestRaceFailureIsNeverFlaky(t *testing.T) {
	f := touched(t)
	f.commitInRepo(t, "app", map[string]string{"r_test.go": "package app\n\nfunc TestRacy(t *testing.T) {}\n"}, "racy")
	out := "==================\nWARNING: DATA RACE\n--- FAIL: TestRacy (0.00s)\n    testing.go:1: race detected during execution of test\nFAIL\nFAIL\texample.com/app\t0.1s\n"
	script := filepath.Join(t.TempDir(), "out.txt")
	writeFile(t, filepath.Dir(script), "out.txt", out)
	eng := fakeEngine(t, map[string]any{"app": report("full", cmdOf("unit", "full", "cat "+script+"; exit 1"))})
	res := runFixture(t, f, eng, nil)
	u := unitOf(t, res, "TestRacy")
	if u.Status != "fail" || !strings.Contains(u.Detail, "data race") {
		t.Errorf("racy unit = %+v", u)
	}
}

// TestFastForwardOntoAMergeTip: a fast-forward onto a default tip that is
// itself a merge commit is a fast-forward, and the repo stays untouched.
func TestFastForwardOntoAMergeTip(t *testing.T) {
	f := newFixture(t, map[string]map[string]string{"app": {"a.txt": "base\n"}})
	seed := f.seeds["app"]
	gitT(t, seed, "checkout", "-q", "-b", "side")
	writeFile(t, seed, "side.txt", "side\n")
	commitAll(t, seed, "side")
	gitT(t, seed, "checkout", "-q", "main")
	writeFile(t, seed, "main.txt", "main\n")
	commitAll(t, seed, "main")
	gitT(t, seed, "merge", "-q", "--no-ff", "--no-edit", "side")
	gitT(t, seed, "push", "-q", "origin", "main")
	res := runFixture(t, f, trivialPlan(t, "app"), nil)
	if m := res.MergeOutcomes["app"]; m.Outcome != MergeFastForward || m.Touched {
		t.Errorf("merge = %+v, want an untouched fast-forward", m)
	}
}

// TestCommandGroupDiesWithTheCommand: a background job a command leaves
// behind is killed when the command ends.
func TestCommandGroupDiesWithTheCommand(t *testing.T) {
	f := touched(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	cmd := "(while :; do :; done) & echo $! > " + pidFile + "; echo started"
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": report("full", cmdOf("bg", "full", cmd))}), nil)
	if c := checkOf(t, res, "app:bg"); c.Status != "pass" {
		t.Fatalf("check = %+v", c)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the background job %d outlived its command", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestScopedWithNothingTouchedIsNotScoped: a scoped request with no
// touched repo proves no scoped run.
func TestScopedWithNothingTouchedIsNotScoped(t *testing.T) {
	f := newFixture(t, map[string]map[string]string{"app": {"a.txt": "base\n"}})
	res := runFixture(t, f, fakeEngine(t, map[string]any{}), func(o *Options) { o.Tier = TierScoped })
	if res.Tier.Effective != TierFull {
		t.Errorf("tier = %+v", res.Tier)
	}
}
