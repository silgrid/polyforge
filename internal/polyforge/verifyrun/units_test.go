package verifyrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func lines(s string) []string { return strings.Split(strings.TrimPrefix(s, "\n"), "\n") }

func mustUnit(t *testing.T, us []*unit, kind, name string) *unit {
	t.Helper()
	u := findUnit(us, kind, name)
	if u == nil {
		var got []string
		for _, x := range us {
			got = append(got, x.Kind+":"+x.Name)
		}
		t.Fatalf("no %s unit %q in %v", kind, name, got)
	}
	return u
}

// TestParseGoNonVerbose: the `go test ./...` shape — failures attributed
// to the package summary that follows them, passing packages recorded,
// a build failure and a test-less package failure as package units.
func TestParseGoNonVerbose(t *testing.T) {
	out := `
--- FAIL: TestAlpha (0.00s)
    alpha_test.go:12: want 3, got 4
FAIL
FAIL	example.com/m/alpha	0.004s
ok  	example.com/m/beta	0.010s
# example.com/m/gamma
gamma/g.go:3:1: syntax error
FAIL	example.com/m/gamma [build failed]
panic: test timed out after 1s
FAIL	example.com/m/delta	1.002s
?   	example.com/m/cmd	[no test files]`
	us := parseUnits(lines(out))
	a := mustUnit(t, us, KindGo, "TestAlpha")
	if !a.Failed || a.Pkg != "example.com/m/alpha" || a.Signature != "alpha_test.go:N: want 3, got 4" {
		t.Errorf("TestAlpha = %+v", a)
	}
	if findUnit(us, KindGoPkg, "example.com/m/alpha") != nil {
		t.Error("a package whose failure is explained by its tests is no unit of its own")
	}
	if b := mustUnit(t, us, KindGoPkg, "example.com/m/beta"); b.Failed {
		t.Error("ok package must pass")
	}
	if g := mustUnit(t, us, KindGoPkg, "example.com/m/gamma"); !g.Failed || !g.BuildFailed {
		t.Errorf("build failure = %+v", g)
	}
	if d := mustUnit(t, us, KindGoPkg, "example.com/m/delta"); !d.Failed || d.BuildFailed {
		t.Errorf("timed-out package = %+v", d)
	}
}

// TestParseGoVerboseSignature: under -v the test's output precedes its
// FAIL header; subtests stay inside their parent unit.
func TestParseGoVerboseSignature(t *testing.T) {
	out := `
=== RUN   TestBeta
=== RUN   TestBeta/case_1
    beta_test.go:40: boom at port 34567
--- FAIL: TestBeta (0.01s)
    --- FAIL: TestBeta/case_1 (0.00s)
=== RUN   TestGamma
--- PASS: TestGamma (0.00s)
FAIL
FAIL	example.com/m/beta	0.02s`
	us := parseUnits(lines(out))
	b := mustUnit(t, us, KindGo, "TestBeta")
	if !b.Failed || b.Signature != "beta_test.go:N: boom at port 34567" {
		t.Errorf("TestBeta = %+v", b)
	}
	if findUnit(us, KindGo, "TestBeta/case_1") != nil {
		t.Error("an indented subtest header is not a top-level unit")
	}
	if g := mustUnit(t, us, KindGo, "TestGamma"); g.Failed || g.Pkg != "example.com/m/beta" {
		t.Errorf("TestGamma = %+v", g)
	}
}

// TestParseHarnessFailLines: the e2e harness prints `FAIL <Test>` per
// failed process and later dumps its -v log; the two are one unit and
// the dump supplies the signature.
func TestParseHarnessFailLines(t *testing.T) {
	out := "ok  \texample.com/m/unit\t0.1s\n" +
		"FAIL TestW1Thing\n" +
		"slowest:\n" +
		"=== output of TestW1Thing\n" +
		"=== RUN   TestW1Thing\n" +
		"    w1_test.go:9: daemon never came up\n" +
		"--- FAIL: TestW1Thing (3.00s)\n" +
		"FAIL\n" +
		"FAIL\te2e\t5/5 tests ran, failed: TestW1Thing \t9s (E2E_JOBS=4)"
	us := parseUnits(lines(out))
	var n int
	for _, u := range us {
		if u.Kind == KindGo && u.Name == "TestW1Thing" {
			n++
			if !u.Failed || u.Signature != "w1_test.go:N: daemon never came up" {
				t.Errorf("harness unit = %+v", u)
			}
		}
	}
	if n != 1 {
		t.Errorf("TestW1Thing parsed %d times, want once", n)
	}
	if findUnit(us, KindGoPkg, "e2e") != nil {
		t.Error("the harness summary's failure is explained by its tests")
	}
}

func TestParsePytest(t *testing.T) {
	out := `
tests/test_a.py::test_ok PASSED                                [ 33%]
tests/test_a.py::TestC::test_bad FAILED                         [ 66%]
_____________________________ TestC.test_bad _____________________________
    def test_bad(self):
>       assert 1 == 2
E       assert 1 == 2
=========================== short test summary info ============================
FAILED tests/test_a.py::TestC::test_bad - assert 1 == 2
FAILED tests/test_b.py::test_other`
	us := parseUnits(lines(out))
	if u := mustUnit(t, us, KindPytest, "tests/test_a.py::test_ok"); u.Failed {
		t.Error("PASSED node must pass")
	}
	if u := mustUnit(t, us, KindPytest, "tests/test_a.py::TestC::test_bad"); !u.Failed || u.Signature != "assert 1 == 2" {
		t.Errorf("failed node = %+v", u)
	}
	if u := mustUnit(t, us, KindPytest, "tests/test_b.py::test_other"); !u.Failed {
		t.Errorf("summary-only failure = %+v", u)
	}
}

func TestParseUnittest(t *testing.T) {
	out := `
test_ok (pkg.test_m.TestM.test_ok) ... ok
======================================================================
FAIL: test_bad (pkg.test_m.TestM)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "pkg/test_m.py", line 7, in test_bad
    self.assertEqual(1, 2)
AssertionError: 1 != 2
----------------------------------------------------------------------
Ran 2 tests in 0.001s`
	us := parseUnits(lines(out))
	if u := mustUnit(t, us, KindUnittest, "pkg.test_m.TestM.test_ok"); u.Failed {
		t.Error("ok case must pass")
	}
	if u := mustUnit(t, us, KindUnittest, "pkg.test_m.TestM.test_bad"); !u.Failed || u.Signature != "AssertionError: 1 != 2" {
		t.Errorf("FAIL case = %+v", u)
	}
}

func TestParseLibtest(t *testing.T) {
	out := `
running 2 tests
test tests::good ... ok
test tests::bad ... FAILED

failures:

---- tests::bad stdout ----
thread 'tests::bad' panicked at src/lib.rs:10:9:
assertion failed: 1 == 2
note: run with RUST_BACKTRACE=1

failures:
    tests::bad`
	us := parseUnits(lines(out))
	if u := mustUnit(t, us, KindLibtest, "tests::good"); u.Failed {
		t.Error("ok test must pass")
	}
	if u := mustUnit(t, us, KindLibtest, "tests::bad"); !u.Failed || u.Signature != "assertion failed: 1 == 2" {
		t.Errorf("FAILED test = %+v", u)
	}
}

func TestParseMjsMarkers(t *testing.T) {
	out := `
PASS: MP1 opens the console
FAIL: MP2 renders the list
  expected 3 rows, saw 0
PASS: MP3 pages`
	us := parseUnits(lines(out))
	if u := mustUnit(t, us, KindMjs, "MP1 opens the console"); u.Failed {
		t.Error("PASS marker must pass")
	}
	if u := mustUnit(t, us, KindMjs, "MP2 renders the list"); !u.Failed || u.Signature != "expected 3 rows, saw 0" {
		t.Errorf("FAIL marker = %+v", u)
	}
}

// TestParseNothingLocatable: output with no recognised line answers no
// unit — the caller turns a failed command into one whole-command unit.
func TestParseNothingLocatable(t *testing.T) {
	if us := parseUnits(lines("make: *** [lint] Error 1\nsomething broke")); len(us) != 0 {
		t.Errorf("units = %v, want none", us)
	}
}

// TestParseGoSameNameTwoPackages: a test name two packages share is two
// units, each in its own package — a pass in one never hides a failure
// in the other.
func TestParseGoSameNameTwoPackages(t *testing.T) {
	out := `
=== RUN   TestNew
--- PASS: TestNew (0.00s)
PASS
ok  	example.com/m/b	0.01s
=== RUN   TestNew
    new_test.go:5: broken in a
--- FAIL: TestNew (0.00s)
FAIL
FAIL	example.com/m/a	0.01s
--- FAIL: TestNew (0.00s)
    new_test.go:9: broken in c
FAIL
FAIL	example.com/m/c	0.01s`
	us := parseUnits(lines(out))
	got := map[string]bool{}
	for _, u := range us {
		if u.Kind == KindGo && u.Name == "TestNew" {
			got[u.Pkg] = u.Failed
		}
	}
	want := map[string]bool{"example.com/m/b": false, "example.com/m/a": true, "example.com/m/c": true}
	if len(got) != 3 {
		t.Fatalf("TestNew units by package = %v, want %v", got, want)
	}
	for p, f := range want {
		if got[p] != f {
			t.Errorf("TestNew in %s failed = %v, want %v", p, got[p], f)
		}
	}
}

// TestShellCommentIsNotSimple: nothing is appended after a comment.
func TestShellCommentIsNotSimple(t *testing.T) {
	if _, simple := shellWords("go test ./... # all"); simple {
		t.Error("a command with a comment must not count as simple")
	}
	if w, simple := shellWords("echo a#b"); !simple || w[1] != "a#b" {
		t.Errorf("a # inside a word is literal: %q %v", w, simple)
	}
}

// TestMjsRerunPassesOnlyWhole: a suite re-run passes a title only when
// the whole suite exits 0.
func TestMjsRerunPassesOnlyWhole(t *testing.T) {
	u := &unit{Kind: KindMjs, Name: "A"}
	ls := lines("PASS: A\nTypeError: boom")
	if unitPassed(u, 1, parseUnits(ls), ls) {
		t.Error("a crash after the PASS line is no pass")
	}
	if !unitPassed(u, 0, parseUnits(ls), ls) {
		t.Error("a clean whole run passes")
	}
}

// TestGoSignatureCoversTheWholeFailure: the signature is every assertion
// line of the test and its subtests — a t.Log before the t.Fatal included,
// message content kept, only source positions normalized — in both the -v
// and the non-verbose shape.
func TestGoSignatureCoversTheWholeFailure(t *testing.T) {
	verbose := `
=== RUN   TestSub
=== RUN   TestSub/one
    sub_test.go:10: setting up after 1.5s
    sub_test.go:12: got 3, want 4
=== RUN   TestSub/two
--- FAIL: TestSub (0.00s)
    --- FAIL: TestSub/one (0.00s)
    --- PASS: TestSub/two (0.00s)
FAIL
FAIL	example.com/m	0.01s`
	u := mustUnit(t, parseUnits(lines(verbose)), KindGo, "TestSub")
	want := "sub_test.go:N: setting up after 1.5s | sub_test.go:N: got 3, want 4"
	if u.Signature != want {
		t.Errorf("-v signature = %q, want %q", u.Signature, want)
	}
	plain := `
--- FAIL: TestSub (0.00s)
    --- FAIL: TestSub/one (0.00s)
        sub_test.go:11: setting up after 1.5s
        sub_test.go:13: got 3, want 4
FAIL
FAIL	example.com/m	0.01s`
	if got := mustUnit(t, parseUnits(lines(plain)), KindGo, "TestSub").Signature; got != want {
		t.Errorf("plain signature = %q, want %q (same failure, other positions)", got, want)
	}
	other := strings.Replace(verbose, "got 3", "got 5", 1)
	if got := mustUnit(t, parseUnits(lines(other)), KindGo, "TestSub").Signature; got == want {
		t.Error("a different message digit must make a different signature")
	}
}

// TestCapText: signatures and details are capped on a rune boundary.
func TestCapText(t *testing.T) {
	long := strings.Repeat("é", 4000)
	got := capText(long)
	if len(got) > maxTextBytes || !strings.HasSuffix(got, "…[cut]") || !utf8.ValidString(got) {
		t.Errorf("capText: len %d, valid %v", len(got), utf8.ValidString(got))
	}
	if capText("short") != "short" {
		t.Error("a short text is kept")
	}
}

// TestSignatureKeepsMessagesAndBodies: message content is never
// normalized (`user.id:42`, `12ms`), a multi-line failure body (a diff)
// is part of the signature, and a data race contributes its report head
// and first frame.
func TestSignatureKeepsMessagesAndBodies(t *testing.T) {
	if normSig("x_test.go:12: user.id:42 latency 12ms") != "x_test.go:N: user.id:42 latency 12ms" {
		t.Errorf("normSig = %q", normSig("x_test.go:12: user.id:42 latency 12ms"))
	}
	if normSig("latency 12ms") == normSig("latency 900ms") {
		t.Error("a duration inside a message is content")
	}
	diff := func(got string) string {
		return "--- FAIL: TestDiff (0.00s)\n    d_test.go:9: mismatch (-want +got):\n          map[string]int{\n        -   \"a\": 1,\n        +   \"a\": " + got + ",\n          }\nFAIL\nFAIL\texample.com/m\t0.01s"
	}
	a := mustUnit(t, parseUnits(lines(diff("2"))), KindGo, "TestDiff").Signature
	b := mustUnit(t, parseUnits(lines(diff("3"))), KindGo, "TestDiff").Signature
	if a == b || !strings.Contains(a, `+ "a": 2,`) {
		t.Errorf("a diff body must be part of the signature: %q vs %q", a, b)
	}
	race := "=== RUN   TestRace\n==================\nWARNING: DATA RACE\nWrite at 0x00c000012345 by goroutine 8:\n  example.com/m.TestRace.func1()\n      /src/r_test.go:10 +0x44\n==================\n    testing.go:1490: race detected during execution of test\n--- FAIL: TestRace (0.00s)\nFAIL\nFAIL\texample.com/m\t0.01s"
	if sig := mustUnit(t, parseUnits(lines(race)), KindGo, "TestRace").Signature; !strings.Contains(sig, "WARNING: DATA RACE | example.com/m.TestRace.func1()") {
		t.Errorf("race signature = %q", sig)
	}
}

// TestTestifySignatureAcrossRuns: real testify output (captured from a
// throwaway module): the same failure run in the repo and in a base
// worktree signs the same once the run roots are tokens, its
// expected/actual body is part of the signature, and a different actual
// value signs differently.
func TestTestifySignatureAcrossRuns(t *testing.T) {
	read := func(name, root string) string {
		raw, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return mustUnit(t, parseUnits(normalizeOutput(lines(string(raw)), root, "")), KindGo, "TestEq").Signature
	}
	head := read("testify-plain.txt", "/work/repo")
	base := read("testify-v.txt", "/base/wt")
	other := read("testify-v-other.txt", "/base/wt")
	if head != base {
		t.Errorf("the same testify failure signs differently:\n %q\n %q", head, base)
	}
	if !strings.Contains(head, "Error Trace: <root>/a_test.go:N") || !strings.Contains(head, "actual : 4") {
		t.Errorf("testify signature lacks the normalized trace or its body: %q", head)
	}
	if other == base {
		t.Error("a different actual value must sign differently")
	}
}

// TestPackageThatDidNotFinishIsAUnit: a failing package whose binary did
// not finish normally — a panic, a timeout, an os.Exit, -failfast — is a
// failing unit beside its failed tests.
func TestPackageThatDidNotFinishIsAUnit(t *testing.T) {
	head := "--- FAIL: TestA (0.00s)\n    a_test.go:9: old breakage\n"
	for name, c := range map[string]struct {
		out      string
		failfast bool
		want     bool
	}{
		"finished":        {head + "FAIL\nFAIL\texample.invalid/x\t0.003s", false, false},
		"goroutine panic": {head + "panic: boom in goroutine\nexample.invalid/x.TestZ.func1()\nFAIL\texample.invalid/x\t0.005s", false, true},
		"timeout":         {head + "panic: test timed out after 3s\nFAIL\texample.invalid/x\t3.019s", false, true},
		"os.Exit":         {head + "FAIL\texample.invalid/x\t0.003s", false, true},
		"failfast":        {head + "FAIL\nFAIL\texample.invalid/x\t0.003s", true, true},
	} {
		us := parseUnitsOpts(lines(c.out), parseOpts{failfast: c.failfast})
		u := findUnit(us, KindGoPkg, "example.invalid/x")
		if got := u != nil && u.Failed; got != c.want {
			t.Errorf("%s: package unit = %+v, want failing=%v", name, u, c.want)
		}
		if a := findUnit(us, KindGo, "TestA"); a == nil || !a.Failed {
			t.Errorf("%s: TestA must stay a failing unit", name)
		}
	}
}

// TestNormSigDropsTheTempDirSuffix: only the random suffix of a t.TempDir()
// path under a run token goes; the test name and sequence stay.
func TestNormSigDropsTheTempDirSuffix(t *testing.T) {
	a := normSig("x_test.go:5: open <tmp>/TestOld2938475/001/data: missing")
	b := normSig("x_test.go:7: open <tmp>/TestOld11/001/data: missing")
	if a != b || a != "x_test.go:N: open <tmp>/TestOld/001/data: missing" {
		t.Errorf("normSig = %q vs %q", a, b)
	}
	if normSig("open <tmp>/TestOld1/002/data") == normSig("open <tmp>/TestOld1/001/data") {
		t.Error("the TempDir sequence number is kept")
	}
}
