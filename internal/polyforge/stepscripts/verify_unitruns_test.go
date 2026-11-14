package stepscripts

// verify_unitruns_test.go: verify.py --unit-runs (workflow contract v2
// PR 12; now the script) — the build's recorded green run of each unit
// (artifacts/build/units.json `[{id, test_commit, impl_commit, run{repo,
// cwd, argv}, marker}]`) run again as written, beside the light plan, each
// one check under details.checks[] with its unit under units[]: a pass
// (the argv verbatim from the run's cwd, the marker matched), a fail (a
// red run, a pass whose output never shows the marker), a flaky first run
// that passes on its re-run, a run naming a repo the capsule does not
// carry, and the invocations refused (an unreadable units file).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unitRunsFile writes units.json under the capsule's artifacts/build and
// answers its path.
func unitRunsFile(t *testing.T, f *capsuleFixture, units []map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(units)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.dir, "artifacts", "build", "units.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// unitRunOf is one units.json entry of repo app.
func unitRunOf(id, repo, cwd, marker string, argv ...string) map[string]any {
	return map[string]any{"id": id, "test_commit": "", "impl_commit": "", "run": map[string]any{"repo": repo, "cwd": cwd, "argv": argv}, "marker": marker}
}

// checkByID answers the details' check of that id (nil when absent).
func checkByID(d *verifyDetails, id string) *verifyCheck {
	for i := range d.Checks {
		if d.Checks[i].ID == id {
			return &d.Checks[i]
		}
	}
	return nil
}

func TestVerifyUnitRunsPass(t *testing.T) {
	f := verifyCapsule(t)
	f.change(t, map[string]string{"sub/x_test.sh": "echo x\n"})
	engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo ok"))})
	units := unitRunsFile(t, f, []map[string]any{
		unitRunOf("u1", "app", "sub", "^UNIT-U1-PASS$", "sh", "-c", `test -f x_test.sh && pwd && echo "UNIT-U1-PASS"`),
	})
	exit, stdout, errb := newVerifyWorld(t).run(t, verifyArgs(f, engine, "--unit-runs", units))
	if exit != 0 {
		t.Fatalf("exit %d\n%s\n%s", exit, stdout, errb)
	}
	res := readResult(t, filepath.Join(f.dir, "artifacts", "verify", "result.json"))
	var d verifyDetails
	detailsOf(t, res, &d)
	c := checkByID(&d, "app:unit-run:u1")
	if c == nil {
		t.Fatalf("no check for the unit run: %+v", d.Checks)
	}
	if c.Repo != "app" || c.Cwd != "sub" || strings.Join(c.Argv, "\x00") != strings.Join([]string{"sh", "-c", `test -f x_test.sh && pwd && echo "UNIT-U1-PASS"`}, "\x00") ||
		c.Status != "pass" || c.Exit == nil || *c.Exit != 0 || c.Log == "" {
		t.Fatalf("the unit run's check (argv verbatim, from its cwd, exit 0): %+v", c)
	}
	if len(c.Units) != 1 || c.Units[0].Unit != "u1" || c.Units[0].Kind != "unit_run" || c.Units[0].Status != "pass" || c.Units[0].Flaky || c.Units[0].PreExisting {
		t.Fatalf("the check's unit: %+v", c.Units)
	}
	log, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(c.Log)))
	if err != nil || !strings.Contains(string(log), "/repos/app/sub\n") || !strings.Contains(string(log), "UNIT-U1-PASS") {
		t.Fatalf("the run ran from repos/app/sub: %v\n%s", err, log)
	}
	// the plan still ran beside it
	if p := checkByID(&d, "app:unit"); p == nil || p.Status != "pass" {
		t.Fatalf("the light plan's check: %+v", d.Checks)
	}
	if res.Verdict != VerdictPass {
		t.Fatalf("verdict %s", res.Verdict)
	}
}

func TestVerifyUnitRunsFail(t *testing.T) {
	for name, c := range map[string]struct {
		marker string
		argv   []string
		detail string
	}{
		"red run":     {"^OK$", []string{"sh", "-c", "echo 'FAIL: TestAnswer'; exit 1"}, "exit 1"},
		"marker miss": {"^UNIT-U1-PASS$", []string{"sh", "-c", "echo 'ok (no tests to run)'"}, "marker"},
	} {
		t.Run(name, func(t *testing.T) {
			f := verifyCapsule(t)
			engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo ok"))})
			units := unitRunsFile(t, f, []map[string]any{unitRunOf("u1", "app", "", c.marker, c.argv...)})
			exit, _, errb := newVerifyWorld(t).run(t, verifyArgs(f, engine, "--unit-runs", units))
			if exit != 1 {
				t.Fatalf("exit %d (want 1: the unit run is red)\n%s", exit, errb)
			}
			res := readResult(t, filepath.Join(f.dir, "artifacts", "verify", "result.json"))
			var d verifyDetails
			detailsOf(t, res, &d)
			ck := checkByID(&d, "app:unit-run:u1")
			if ck == nil || ck.Status != "fail" || !strings.Contains(ck.Detail, c.detail) {
				t.Fatalf("the unit run's check must fail naming %q: %+v", c.detail, ck)
			}
			if len(ck.Units) != 1 || ck.Units[0].Unit != "u1" || ck.Units[0].Status != "fail" || ck.Units[0].PreExisting {
				t.Fatalf("the failing unit (never pre-existing: it pins this change): %+v", ck.Units)
			}
			if res.Verdict != VerdictFail || !strings.Contains(res.Summary, "app:unit-run:u1") {
				t.Fatalf("verdict %s, summary %q", res.Verdict, res.Summary)
			}
		})
	}
}

// A first run that fails and a re-run that passes: the unit passes on
// record as flaky (the light tier's re-run policy).
func TestVerifyUnitRunsFlakyPassesOnRerun(t *testing.T) {
	f := verifyCapsule(t)
	engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo ok"))})
	seen := filepath.Join(t.TempDir(), "seen")
	units := unitRunsFile(t, f, []map[string]any{
		unitRunOf("u1", "app", ".", "^PASS$", "sh", "-c", "if [ -e "+seen+" ]; then echo PASS; else : > "+seen+"; echo 'FAIL: first'; exit 1; fi"),
	})
	exit, _, errb := newVerifyWorld(t).run(t, verifyArgs(f, engine, "--unit-runs", units))
	if exit != 0 {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	res := readResult(t, filepath.Join(f.dir, "artifacts", "verify", "result.json"))
	var d verifyDetails
	detailsOf(t, res, &d)
	ck := checkByID(&d, "app:unit-run:u1")
	if ck == nil || ck.Status != "pass" || len(ck.Units) != 1 || ck.Units[0].Status != "flaky" || !ck.Units[0].Flaky {
		t.Fatalf("the flaky unit run: %+v", ck)
	}
}

// A run naming a repo the capsule does not carry cannot run as written:
// its check fails (the build's units.json is wrong), the plan still runs.
func TestVerifyUnitRunsMissingRepo(t *testing.T) {
	f := verifyCapsule(t)
	engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo ok"))})
	units := unitRunsFile(t, f, []map[string]any{
		unitRunOf("u1", "ghost", "", "^hi$", "sh", "-c", "echo hi"),
		unitRunOf("u2", "app", "", "^U2-PASS$", "sh", "-c", "echo U2-PASS"),
	})
	exit, _, errb := newVerifyWorld(t).run(t, verifyArgs(f, engine, "--unit-runs", units))
	if exit != 1 {
		t.Fatalf("exit %d (want 1)\n%s", exit, errb)
	}
	res := readResult(t, filepath.Join(f.dir, "artifacts", "verify", "result.json"))
	var d verifyDetails
	detailsOf(t, res, &d)
	ck := checkByID(&d, "ghost:unit-run:u1")
	if ck == nil || ck.Status != "fail" || ck.Repo != "ghost" || ck.Exit != nil || !strings.Contains(ck.Detail, "repos/ghost is not a task repo") {
		t.Fatalf("the unit run naming a missing repo: %+v (checks %+v)", ck, d.Checks)
	}
	if len(ck.Units) != 1 || ck.Units[0].Unit != "u1" || ck.Units[0].Status != "fail" {
		t.Fatalf("its unit: %+v", ck.Units)
	}
	if ok := checkByID(&d, "app:unit-run:u2"); ok == nil || ok.Status != "pass" {
		t.Fatalf("the other unit run still runs: %+v", ok)
	}
	if p := checkByID(&d, "app:unit"); p == nil || p.Status != "pass" {
		t.Fatalf("the plan still runs: %+v", p)
	}
}

// An unusable --unit-runs is the invocation's: exit 2, nothing run. Each
// case its own capsule and path (a file one case leaves must not answer
// for the next).
func TestVerifyUnitRunsRefusesAnUnreadableFile(t *testing.T) {
	for _, c := range []struct{ name, body string }{{"missing", ""}, {"not json", "{not json"}, {"not an array", `{"id":"u1"}`}} {
		t.Run(c.name, func(t *testing.T) {
			f := verifyCapsule(t)
			engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo ok"))})
			path := filepath.Join(t.TempDir(), "units.json")
			if c.body != "" {
				if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			exit, _, errb := newVerifyWorld(t).run(t, verifyArgs(f, engine, "--unit-runs", path))
			if exit != 2 || !strings.Contains(errb, "--unit-runs") {
				t.Errorf("exit %d (want 2), stderr %q", exit, errb)
			}
		})
	}
}

// A unit run that cannot run as written fails its check without running:
// no marker, a blank one, or one the empty string matches (it would pass
// on exit 0 alone — a filter that ran no test exits 0 too), a marker that
// is no Python regular expression (re.ASCII), an empty argv, a cwd outside
// the repo or missing at HEAD. The plan still runs beside it.
func TestVerifyUnitRunsRefusesWhatCannotRunAsWritten(t *testing.T) {
	for _, c := range []struct {
		name, cwd, marker string
		argv              []string
		detail            string
	}{
		{"empty marker", "", "", nil, "no marker"},
		{"whitespace marker", "", "  \t ", nil, "no marker"},
		{"marker matching every line", "", ".*", nil, "matches the empty string"},
		{"anchor-only marker", "", "^", nil, "matches the empty string"},
		{"starred marker", "", "a*", nil, "matches the empty string"},
		{"marker that is no regexp", "", "(", nil, "is not a regexp"},
		{"empty argv", "", "^OK$", []string{}, "no argv"},
		{"cwd above the repo", "../", "^OK$", nil, "is not a directory inside the repo"},
		{"absolute cwd", "/etc", "^OK$", nil, "is not a directory inside the repo"},
		{"missing cwd", "no/such/dir", "^OK$", nil, "is no directory of repos/app at HEAD"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := verifyCapsule(t)
			engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo ok"))})
			sentinel := filepath.Join(t.TempDir(), "ran")
			argv := c.argv
			if argv == nil {
				argv = []string{"sh", "-c", "touch " + sentinel + "; echo OK"}
			}
			units := unitRunsFile(t, f, []map[string]any{unitRunOf("u1", "app", c.cwd, c.marker, argv...)})
			exit, _, errb := newVerifyWorld(t).run(t, verifyArgs(f, engine, "--unit-runs", units))
			if exit != 1 {
				t.Fatalf("exit %d (want 1: the unit run cannot run as written)\n%s", exit, errb)
			}
			res := readResult(t, filepath.Join(f.dir, "artifacts", "verify", "result.json"))
			var d verifyDetails
			detailsOf(t, res, &d)
			ck := checkByID(&d, "app:unit-run:u1")
			if ck == nil || ck.Status != "fail" || ck.Exit != nil || !strings.Contains(ck.Detail, c.detail) {
				t.Fatalf("the check must fail unrun naming %q: %+v", c.detail, ck)
			}
			if len(ck.Units) != 1 || ck.Units[0].Unit != "u1" || ck.Units[0].Status != "fail" {
				t.Fatalf("its unit: %+v", ck.Units)
			}
			if _, err := os.Stat(sentinel); err == nil {
				t.Fatal("the unit run ran although it cannot run as written")
			}
			if p := checkByID(&d, "app:unit"); p == nil || p.Status != "pass" {
				t.Fatalf("the plan still runs: %+v", p)
			}
		})
	}
}
