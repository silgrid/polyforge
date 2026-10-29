package verifyrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// harnessRepo is a repo run whose conf declares the Go harness.
func harnessRepo() *repoRun {
	return &repoRun{name: "app", goHarness: "test/e2e-parallel.sh"}
}

// proofOf runs completionProof over a command and its output.
func proofOf(t *testing.T, cmd, output string) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "1.log")
	if err := os.WriteFile(logPath, []byte("$ header\n# cwd: x\n"+output+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return completionProof(&cmdRun{check: "app:x", cmd: cmd, logPath: logPath, exit: 1, harness: harnessRepo().isHarness(cmd)})
}

// TestCompletionProofOnlyTheDeclaredHarness: only the declared Go harness,
// run directly, proves completion — and only through a trailer counting
// every listed test; no other runner's output does.
func TestCompletionProofOnlyTheDeclaredHarness(t *testing.T) {
	trailer := "FAIL TestA\nFAIL\te2e\t3/3 tests ran, failed: TestA \t1s (E2E_JOBS=1)"
	cases := []struct {
		name, cmd, out string
		proven         bool
	}{
		{"declared harness", "GOWORK=off sh test/e2e-parallel.sh", trailer, true},
		{"declared harness, ./ path, bash", "bash ./test/e2e-parallel.sh", trailer, true},
		{"declared harness with exclusions", "E2E_SKIP='^(TestB)$' sh test/e2e-parallel.sh", "FAIL\te2e\t2/3 tests ran, 1 excluded by E2E_SKIP, failed: TestA \t1s (E2E_JOBS=1)", true},
		{"harness lost a test", "sh test/e2e-parallel.sh", "FAIL\te2e\t2/3 tests ran, failed: TestA \t1s (E2E_JOBS=1)", false},
		{"trailer not last", "sh test/e2e-parallel.sh", trailer + "\nlint: broken", false},
		{"wrapper script", "bash scripts/ci.sh", trailer, false},
		{"sh -c string", "sh -c 'go test ./...; sh test/e2e-parallel.sh'", trailer, false},
		{"extra argument", "sh test/e2e-parallel.sh -v", trailer, false},
		{"make target", "make test", trailer, false},
		{"go test", "go test -count=1 ./...", "ok  \texample.invalid/a\t0.1s\nFAIL\texample.invalid/b\t0.1s", false},
		{"pytest -x", "pytest -x", "FAILED tests/a.py::test_x - assert 1\n===== 1 failed in 0.12s =====", false},
		{"unittest -f", "python3 -m unittest -f", "Ran 1 test in 0.001s\n\nFAILED (failures=1)", false},
		{"cargo test", "cargo test", "test result: FAILED. 2 passed; 1 failed; 0 ignored", false},
		{"node one file", "node e2e/flow.mjs", "FAIL: A\nTypeError: boom", false},
	}
	for _, c := range cases {
		if got := proofOf(t, c.cmd, c.out); (got != "") != c.proven {
			t.Errorf("%s: proof %q, want proven=%v", c.name, got, c.proven)
		}
	}
}

// TestNoSkipFormStaysFailedWithoutAnyProof: a command carrying only
// pre-existing failures but no skip form stays failed — cargo test, a
// node suite, a wrapper script ending in a harness trailer.
func TestNoSkipFormStaysFailedWithoutAnyProof(t *testing.T) {
	trailer := "FAIL\te2e\t3/3 tests ran, failed: TestA \t1s (E2E_JOBS=1)"
	for _, c := range []struct{ cmd, kind, name, out string }{
		{"cargo test", KindLibtest, "tests::old", "test result: FAILED. 2 passed; 1 failed; 0 ignored"},
		{"node e2e/flow.mjs", KindMjs, "A", "FAIL: A"},
		{"bash scripts/ci.sh", KindGo, "TestA", trailer},
	} {
		logPath := filepath.Join(t.TempDir(), "1.log")
		if err := os.WriteFile(logPath, []byte("$ h\n# c\n"+c.out+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rr := harnessRepo()
		cr := &cmdRun{repo: "app", check: "app:x", cmd: c.cmd, logPath: logPath, exit: 1, harness: rr.isHarness(c.cmd),
			units: []*unitRun{{unit: &unit{Kind: c.kind, Name: c.name, Failed: true}, PreExisting: true}}}
		rr.runs = []*cmdRun{cr}
		r := &runner{o: Options{CapsuleDir: t.TempDir(), Engine: "x"}.withDefaults(),
			deadline: time.Now().Add(time.Hour), hard: time.Now().Add(time.Hour)}
		r.confirmPhase(t.Context(), rr)
		if ck := commandCheck(cr); ck.Status != "fail" || !strings.Contains(ck.Detail, "no skip switch") {
			t.Errorf("%s: check = %+v", c.cmd, ck)
		}
	}
}

// TestHarnessSkipForm: the harness protocol's skip form is E2E_SKIP,
// composing with an existing E2E_RUN; an existing E2E_SKIP is not merged.
func TestHarnessSkipForm(t *testing.T) {
	pre := func() []*unitRun {
		return []*unitRun{{unit: &unit{Kind: KindGo, Name: "TestA", Failed: true}, PreExisting: true},
			{unit: &unit{Kind: KindGo, Name: "TestB", Failed: true}, PreExisting: true}}
	}
	for cmd, want := range map[string]string{
		"GOWORK=off sh test/e2e-parallel.sh":                "E2E_SKIP='^(TestA|TestB)$' GOWORK=off sh test/e2e-parallel.sh",
		"E2E_RUN='^(TestA|TestC)$' sh test/e2e-parallel.sh": "E2E_SKIP='^(TestA|TestB)$' E2E_RUN='^(TestA|TestC)$' sh test/e2e-parallel.sh",
	} {
		if got, note := skipForm(&cmdRun{repo: "app", cmd: cmd, harness: true, units: pre()}); got != want || note != "" {
			t.Errorf("skipForm(%q) = %q (%q), want %q", cmd, got, note, want)
		}
	}
	if _, note := skipForm(&cmdRun{repo: "app", cmd: "E2E_SKIP=x sh test/e2e-parallel.sh", harness: true, units: pre()}); note == "" {
		t.Error("an existing E2E_SKIP must not be merged silently")
	}
	if _, note := skipForm(&cmdRun{repo: "app", cmd: "sh test/e2e-parallel.sh", units: pre()}); note == "" {
		t.Error("without the harness trailer there is no harness skip form")
	}
}

// TestHarnessTrailerNamesItsFailures: every test the trailer names failing
// is a failing unit — one the harness failed without a FAIL line of its
// own (exit 0 without a verdict) included — and adds nothing for tests
// already parsed; a trailer with tests lost is a failure of its own.
func TestHarnessTrailerNamesItsFailures(t *testing.T) {
	out := "FAIL TestA\n=== output of TestA\n=== RUN   TestA\n    a_test.go:3: boom\n--- FAIL: TestA (0.00s)\nFAIL\n=== output of TestQ\n=== RUN   TestQ\n" +
		"FAIL\te2e\t3/3 tests ran, failed: TestA TestQ \t1s (E2E_JOBS=1)"
	us := parseUnits(lines(out))
	if u := findUnit(us, KindGoPkg, "e2e"); u != nil {
		t.Errorf("a complete trailer added %+v", u)
	}
	for _, n := range []string{"TestA", "TestQ"} {
		if u := findUnit(us, KindGo, n); u == nil || !u.Failed {
			t.Errorf("%s must be a failing unit: %+v", n, u)
		}
	}
	lost := "FAIL\te2e\t1/2 tests ran, failed:  \t1s (E2E_JOBS=1)"
	if u := findUnit(parseUnits(lines(lost)), KindGoPkg, "e2e"); u == nil || !u.Failed {
		t.Error("a trailer with tests lost is a failure of its own")
	}
}
