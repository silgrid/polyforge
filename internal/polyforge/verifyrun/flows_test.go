package verifyrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakePytest installs a `pytest` on a private PATH. It reads the
// tree's tests/test_*.py: a `def` line carrying `# FAIL <msg>` fails with
// that message, `# FLAKY` fails until $FLAKY_STATE exists (creating it),
// anything else passes; a node id argument selects one test and
// --deselect drops one; a tests/ENV_BROKEN file makes every run an
// environmental failure, and tests/LINT_BROKEN fails a whole run after the
// tests with nothing a parser locates.
func fakePytest(t *testing.T) []string {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/bash
if [ -f tests/ENV_BROKEN ]; then echo "ImportError: the environment is broken"; exit 4; fi
sel=""; skip=" "; xfirst=""
while [ $# -gt 0 ]; do
  case "$1" in
    --deselect) skip="$skip$2 "; shift 2;;
    -x) xfirst=1; shift;;
    -*) shift;;
    *) sel="$1"; shift;;
  esac
done
fail=0; pass=0
for f in tests/test_*.py; do
  [ -f "$f" ] || continue
  while IFS= read -r line; do
    case "$line" in "def "*) ;; *) continue;; esac
    fn=${line#def }; fn=${fn%%(*}
    id="$f::$fn"
    if [ -n "$sel" ] && [ "$sel" != "$id" ]; then continue; fi
    case "$skip" in *" $id "*) continue;; esac
    case "$line" in
      *"# FAIL "*) echo "FAILED $id - ${line#*# FAIL }"; fail=$((fail+1)); [ -n "$xfirst" ] && break 2;;
      *"# FLAKY2"*) n=$(cat "$FLAKY_STATE.n" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" >"$FLAKY_STATE.n"
        if [ "$n" -ge 3 ]; then echo "$id PASSED"; pass=$((pass+1)); else echo "FAILED $id - assert flaky twice"; fail=$((fail+1)); fi;;
      *"# FLAKY"*) if [ -f "$FLAKY_STATE" ]; then echo "$id PASSED"; pass=$((pass+1)); else : >"$FLAKY_STATE"; echo "FAILED $id - assert flaky"; fail=$((fail+1)); fi;;
      *) echo "$id PASSED"; pass=$((pass+1));;
    esac
  done <"$f"
done
echo "$pass passed, $fail failed"
if [ -z "$sel" ] && [ -f tests/LINT_BROKEN ]; then echo "lint: something no parser locates"; exit 1; fi
[ "$fail" -eq 0 ]
`
	if err := os.WriteFile(filepath.Join(bin, "pytest"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return setEnv(os.Environ(), "PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func unitOf(t *testing.T, res *Result, name string) UnitReport {
	t.Helper()
	for _, u := range res.Units {
		if u.Name == name {
			return u
		}
	}
	t.Fatalf("no unit %q in %+v", name, res.Units)
	return UnitReport{}
}

// pytestFixture: base has tests/test_a.py (baseA); the feature branch
// writes the given files.
func pytestFixture(t *testing.T, baseFiles, feature map[string]string) (*fixture, string) {
	t.Helper()
	f := newFixture(t, map[string]map[string]string{"app": baseFiles})
	f.commitInRepo(t, "app", feature, "feature")
	return f, fakeEngine(t, map[string]any{"app": report("full", cmdOf("tests", "full", "pytest"))})
}

// TestFlakyUnitPassesOnRerun: a unit failing once, then passing on its
// targeted re-run at HEAD, is a pass recorded flaky n/m.
func TestFlakyUnitPassesOnRerun(t *testing.T) {
	f, eng := pytestFixture(t, map[string]string{"tests/test_a.py": "def test_ok():\n"},
		map[string]string{"tests/test_a.py": "def test_ok():\ndef test_wobbly(): # FLAKY\n"})
	env := setEnv(fakePytest(t), "FLAKY_STATE", filepath.Join(t.TempDir(), "flaky"))
	res := runFixture(t, f, eng, func(o *Options) { o.Env = env })
	u := unitOf(t, res, "tests/test_a.py::test_wobbly")
	if u.Status != "flaky" || !strings.Contains(u.Detail, "flaky 1/2") {
		t.Errorf("unit = %+v", u)
	}
	if c := checkOf(t, res, "app:tests"); c.Status != "pass" || !strings.Contains(c.Detail, "flaky 1/2") {
		t.Errorf("check = %+v", c)
	}
	if res.MechanicalVerdict != "pass" {
		t.Errorf("verdict = %s (%s)", res.MechanicalVerdict, res.FailReason)
	}
	var rerun bool
	for _, e := range res.CommandEvidence {
		rerun = rerun || strings.Join(e.Argv, " ") == "bash -c pytest tests/test_a.py::test_wobbly"
	}
	if !rerun {
		t.Error("the re-run carries no command evidence")
	}
}

// TestPreExistingProvenOnBase: a failure that exists at the base and fails
// there the same way on every base run is pre-existing — its check,
// its pre_existing_failures entry, the base sha in the detail and the
// --deselect form in the test bar; a test the base does not have is this
// change's failure.
func TestPreExistingProvenOnBase(t *testing.T) {
	f, eng := pytestFixture(t,
		map[string]string{"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"},
		map[string]string{"tests/test_b.py": "def test_new(): # FAIL assert new breakage\n"})
	res := runFixture(t, f, eng, func(o *Options) { o.Env = fakePytest(t) })
	base := res.MergeOutcomes["app"].Base
	old := unitOf(t, res, "tests/test_a.py::test_old")
	if old.Status != "pre_existing" || !strings.Contains(old.Detail, base) {
		t.Errorf("old = %+v", old)
	}
	if nw := unitOf(t, res, "tests/test_b.py::test_new"); nw.Status != "fail" || !strings.Contains(nw.Detail, "base does not have this test") {
		t.Errorf("new = %+v", nw)
	}
	if len(res.PreExisting) != 1 || res.PreExisting[0] != "tests/test_a.py: test_old" {
		t.Errorf("pre_existing_failures = %v", res.PreExisting)
	}
	if c := checkOf(t, res, "app:tests"); c.Status != "fail" || !strings.Contains(c.Detail, "test_new") {
		t.Errorf("a check with a new failure is fail: %+v", c)
	}
	if res.TestBar[0].Command != "pytest --deselect tests/test_a.py::test_old" {
		t.Errorf("test_bar = %+v", res.TestBar)
	}
	// only the pre-existing one: the check itself is pre_existing
	f.commitInRepo(t, "app", map[string]string{"tests/test_b.py": "def test_new():\n"}, "fix")
	res = runFixture(t, f, eng, func(o *Options) { o.Env = fakePytest(t) })
	if c := checkOf(t, res, "app:tests"); c.Status != "pre_existing" || !strings.Contains(c.Detail, base) {
		t.Errorf("check = %+v", c)
	}
	if res.MechanicalVerdict != "pass" {
		t.Errorf("verdict = %s", res.MechanicalVerdict)
	}
	var worktrees int
	for _, l := range strings.Split(gitT(t, f.repo("app"), "worktree", "list"), "\n") {
		if strings.TrimSpace(l) != "" {
			worktrees++
		}
	}
	if worktrees != 1 {
		t.Errorf("the base worktree was not removed:\n%s", gitT(t, f.repo("app"), "worktree", "list"))
	}
}

// TestPreExistingNegativeControls: the three false-positive shapes are
// each a new failure — different signature on the base, an environmental
// failure on the base, and a protected (authored) test.
func TestPreExistingNegativeControls(t *testing.T) {
	t.Run("different signature", func(t *testing.T) {
		f, eng := pytestFixture(t,
			map[string]string{"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"},
			map[string]string{"tests/test_a.py": "def test_old(): # FAIL assert something else\n"})
		res := runFixture(t, f, eng, func(o *Options) { o.Env = fakePytest(t) })
		if u := unitOf(t, res, "tests/test_a.py::test_old"); u.Status != "fail" || !strings.Contains(u.Detail, "fails differently") {
			t.Errorf("unit = %+v", u)
		}
	})
	t.Run("environmental at base", func(t *testing.T) {
		f := newFixture(t, map[string]map[string]string{"app": {"tests/test_a.py": "def test_old(): # FAIL assert old\n", "tests/ENV_BROKEN": "x\n"}})
		if err := os.Remove(filepath.Join(f.repo("app"), "tests/ENV_BROKEN")); err != nil {
			t.Fatal(err)
		}
		f.commitInRepo(t, "app", map[string]string{"c.txt": "x\n"}, "feature")
		eng := fakeEngine(t, map[string]any{"app": report("full", cmdOf("tests", "full", "pytest"))})
		res := runFixture(t, f, eng, func(o *Options) { o.Env = fakePytest(t) })
		if u := unitOf(t, res, "tests/test_a.py::test_old"); u.Status != "fail" || !strings.Contains(u.Detail, "environmental") {
			t.Errorf("unit = %+v", u)
		}
	})
	t.Run("protected authored test", func(t *testing.T) {
		f, eng := pytestFixture(t,
			map[string]string{"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"},
			map[string]string{"c.txt": "x\n"})
		f.execution(t, map[string]map[string]any{"e2e-author": {"scenario_titles": []any{}, "test_files": []any{"tests/test_a.py"}}})
		res := runFixture(t, f, eng, func(o *Options) { o.Env = fakePytest(t) })
		if u := unitOf(t, res, "tests/test_a.py::test_old"); u.Status != "fail" || !u.Protected || !strings.Contains(u.Detail, "protected") {
			t.Errorf("unit = %+v", u)
		}
		if len(res.PreExisting) != 0 {
			t.Errorf("pre_existing_failures = %v", res.PreExisting)
		}
	})
}

// TestUnparseableFailureIsOneUnitNeverPreExisting: a failed command with
// no locatable unit is the whole command, never pre-existing, never
// re-run.
func TestUnparseableFailureIsOneUnitNeverPreExisting(t *testing.T) {
	f := touched(t)
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": report("full", cmdOf("lint", "fast", "echo 'lint: 3 issues'; exit 1"))}), nil)
	u := unitOf(t, res, "app:lint")
	if u.Kind != KindCommand || u.Status != "fail" || !strings.Contains(u.Detail, "never pre-existing") {
		t.Errorf("unit = %+v", u)
	}
}

// TestBindingsResolveAuthoredScenarios: a binding passes on its marker
// in a planned command's output; a binding naming a command the plan did
// not run gets its own authored run (a check and a test-bar entry); a
// missing marker fails; an unbound title is left to the step.
func TestBindingsResolveAuthoredScenarios(t *testing.T) {
	f, _ := pytestFixture(t, map[string]string{"tests/test_a.py": "def test_ok():\n"}, map[string]string{"c.txt": "x\n"})
	f.execution(t, map[string]map[string]any{"e2e-author": {
		"scenario_titles": []any{"S1 planned", "S2 own run", "S3 no marker", "S4 document", "S5 needs env"},
		"bindings": []any{
			map[string]any{"title": "S1 planned", "repo": "app", "command": "pytest", "unit": "tests/test_a.py::test_ok", "marker": `^tests/test_a\.py::test_ok PASSED$`},
			map[string]any{"title": "S2 own run", "repo": "app", "command": "echo 'PASS S2'", "unit": "S2", "marker": `^PASS S2$`},
			map[string]any{"title": "S3 no marker", "repo": "app", "command": "pytest", "unit": "tests/test_a.py::test_ok", "marker": `^NEVER$`},
			map[string]any{"title": "S5 needs env", "repo": "app", "command": "browser-suite", "unit": "B1", "marker": `^PASS B1$`},
		}}})
	needs := cmdOf("browser", "full", "browser-suite")
	needs.RequiresEnv = []string{"VR_NO_BROWSER"}
	eng := fakeEngine(t, map[string]any{"app": report("full", cmdOf("tests", "full", "pytest"), needs)})
	res := runFixture(t, f, eng, func(o *Options) { o.Env = fakePytest(t) })
	if !res.Authored || len(res.AuthoredTitles) != 5 {
		t.Fatalf("authored = %v %v", res.Authored, res.AuthoredTitles)
	}
	want := map[string]string{"S1 planned": "pass", "S2 own run": "pass", "S3 no marker": "fail", "S5 needs env": "skipped"}
	if len(res.BoundScenarios) != 4 {
		t.Fatalf("bound = %+v", res.BoundScenarios)
	}
	for _, s := range res.BoundScenarios {
		if want[s.Title] != s.Status {
			t.Errorf("%s = %s (%s)", s.Title, s.Status, s.Detail)
		}
	}
	if len(res.UnboundTitles) != 1 || res.UnboundTitles[0] != "S4 document" {
		t.Errorf("unbound = %v", res.UnboundTitles)
	}
	if c := checkOf(t, res, "app:authored-1"); c.Status != "pass" {
		t.Errorf("authored run check = %+v", c)
	}
	if len(res.TestBar) != 2 || res.TestBar[1].Command != "echo 'PASS S2'" {
		t.Errorf("test_bar = %+v", res.TestBar)
	}
}

// TestExplainedFailuresAreConfirmedWhole: a command whose located failures
// all turned out flaky is re-run whole; an exit no unit explains (here a
// lint step after the tests) leaves a residual failure — never a pass.
func TestExplainedFailuresAreConfirmedWhole(t *testing.T) {
	f, eng := pytestFixture(t, map[string]string{"tests/test_a.py": "def test_ok():\n"},
		map[string]string{"tests/test_a.py": "def test_ok():\ndef test_wobbly(): # FLAKY\n", "tests/LINT_BROKEN": "x\n"})
	env := setEnv(fakePytest(t), "FLAKY_STATE", filepath.Join(t.TempDir(), "flaky"))
	res := runFixture(t, f, eng, func(o *Options) { o.Env = env })
	if u := unitOf(t, res, "tests/test_a.py::test_wobbly"); u.Status != "flaky" {
		t.Errorf("wobbly = %+v", u)
	}
	c := checkOf(t, res, "app:tests")
	if c.Status != "fail" || !strings.Contains(c.Detail, "a failure no unit locates") {
		t.Errorf("check = %+v", c)
	}
	// without the unlocated failure the whole re-run clears it
	f.commitInRepo(t, "app", map[string]string{"tests/LINT_BROKEN": ""}, "noop")
	if err := os.Remove(filepath.Join(f.repo("app"), "tests/LINT_BROKEN")); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.repo("app"), "commit", "-q", "-am", "fixed lint")
	env = setEnv(fakePytest(t), "FLAKY_STATE", filepath.Join(t.TempDir(), "flaky2"))
	res = runFixture(t, f, eng, func(o *Options) { o.Env = env })
	if c := checkOf(t, res, "app:tests"); c.Status != "pass" || !strings.Contains(c.Detail, "re-run whole as `pytest`: exit 0") {
		t.Errorf("check = %+v", c)
	}
}

// TestPreExistingConfirmedThroughTheBarForm: a pre-existing exclusion is
// confirmed by running the bar's own skip form once.
func TestPreExistingConfirmedThroughTheBarForm(t *testing.T) {
	f, eng := pytestFixture(t,
		map[string]string{"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"},
		map[string]string{"tests/test_b.py": "def test_new():\n"})
	res := runFixture(t, f, eng, func(o *Options) { o.Env = fakePytest(t) })
	c := checkOf(t, res, "app:tests")
	if c.Status != "pre_existing" || !strings.Contains(c.Detail, "re-run whole as `pytest --deselect tests/test_a.py::test_old`: exit 0") {
		t.Errorf("check = %+v", c)
	}
}

// TestFlakyCountsPassesOverRuns: a unit passing on its second re-run is
// one pass in three runs.
func TestFlakyCountsPassesOverRuns(t *testing.T) {
	f, eng := pytestFixture(t, map[string]string{"tests/test_a.py": "def test_ok():\n"},
		map[string]string{"tests/test_a.py": "def test_ok():\ndef test_twice(): # FLAKY2\n"})
	env := setEnv(fakePytest(t), "FLAKY_STATE", filepath.Join(t.TempDir(), "flaky"))
	res := runFixture(t, f, eng, func(o *Options) { o.Env = env })
	if u := unitOf(t, res, "tests/test_a.py::test_twice"); u.Status != "flaky" || !strings.Contains(u.Detail, "flaky 1/3") {
		t.Errorf("unit = %+v", u)
	}
}

// TestPytestStopAtFirstNeverExcusesTheRest: `pytest -x` stopped at a
// pre-existing failure never ran the rest; the confirming whole re-run in
// the skip form runs it and finds the new failure — the check fails.
func TestPytestStopAtFirstNeverExcusesTheRest(t *testing.T) {
	f := newFixture(t, map[string]map[string]string{"app": {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"}})
	f.commitInRepo(t, "app", map[string]string{"tests/test_b.py": "def test_new(): # FAIL assert new breakage\n"}, "feature")
	eng := fakeEngine(t, map[string]any{"app": report("full", cmdOf("tests", "full", "pytest -x"))})
	res := runFixture(t, f, eng, func(o *Options) { o.Env = fakePytest(t) })
	if u := unitOf(t, res, "tests/test_a.py::test_old"); u.Status != "pre_existing" {
		t.Fatalf("old = %+v", u)
	}
	c := checkOf(t, res, "app:tests")
	if c.Status != "fail" || !strings.Contains(c.Detail, "a failure no unit locates") {
		t.Errorf("check = %+v", c)
	}
}

// TestGoPackageStopsNeverHideBehindAnExplainedFailure: a package whose
// one failing test is pre-existing but whose binary did not finish — a
// goroutine panic, a hang past the plan's -timeout, -failfast so a later
// test never ran — keeps the check failed.
func TestGoPackageStopsNeverHideBehindAnExplainedFailure(t *testing.T) {
	base := map[string]string{
		"go.mod":    "module example.invalid/stop\n\ngo 1.21\n",
		"a_test.go": "package stop\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Fatal(\"old breakage\") }\n",
	}
	for name, c := range map[string]struct{ cmd, z string }{
		"goroutine panic": {"GOWORK=off go test -count=1 ./...",
			"package stop\n\nimport \"testing\"\n\nfunc TestZ(t *testing.T) {\n\tdone := make(chan bool)\n\tgo func() { panic(\"boom in goroutine\") }()\n\t<-done\n}\n"},
		"hang past -timeout": {"GOWORK=off go test -count=1 -timeout 3s ./...",
			"package stop\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestZ(t *testing.T) { time.Sleep(8 * time.Second) }\n"},
		"failfast": {"GOWORK=off go test -count=1 -failfast ./...",
			"package stop\n\nimport \"testing\"\n\nfunc TestZ(t *testing.T) { t.Fatal(\"new breakage\") }\n"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, map[string]map[string]string{"app": base})
			f.commitInRepo(t, "app", map[string]string{"z_test.go": c.z}, "feature")
			res := runFixture(t, f, fakeEngine(t, map[string]any{"app": report("full", cmdOf("tests", "full", c.cmd))}), nil)
			if ck := checkOf(t, res, "app:tests"); ck.Status != "fail" || res.MechanicalVerdict != "fail" {
				t.Errorf("check = %+v, verdict %s", ck, res.MechanicalVerdict)
			}
		})
	}
}

// TestTempDirPathsSignAlike: a failure naming a t.TempDir() path signs the
// same in the bar, the HEAD re-run and the base runs — only the random
// suffix differs — so it can be proven pre-existing; the same failure
// with a message that differs elsewhere cannot.
func TestTempDirPathsSignAlike(t *testing.T) {
	old := func(word string) string {
		return "package td\n\nimport (\n\t\"path/filepath\"\n\t\"testing\"\n)\n\nfunc TestOld(t *testing.T) { t.Fatalf(\"open %s: " + word + "\", filepath.Join(t.TempDir(), \"data\")) }\n"
	}
	for name, c := range map[string]struct{ head, want string }{
		"same failure":           {old("missing"), "pre_existing"},
		"message differs a word": {old("absent"), "fail"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, map[string]map[string]string{"app": {
				"go.mod":      "module example.invalid/td\n\ngo 1.21\n",
				"old_test.go": old("missing"),
			}})
			f.commitInRepo(t, "app", map[string]string{"old_test.go": c.head, "new.go": "package td\n"}, "feature")
			eng := fakeEngine(t, map[string]any{"app": report("full", cmdOf("tests", "full", "GOWORK=off go test -count=1 ./..."))})
			res := runFixture(t, f, eng, nil)
			if u := unitOf(t, res, "TestOld"); u.Status != c.want || !strings.Contains(u.Signature, "<tmp>/TestOld/001/data") {
				t.Errorf("TestOld = %+v, want %s with the suffix-free TempDir in its signature", u, c.want)
			}
		})
	}
}
