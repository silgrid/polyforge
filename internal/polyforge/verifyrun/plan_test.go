package verifyrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// touched is a fixture whose one repo carries a commit of its own.
func touched(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, map[string]map[string]string{"app": {"a.txt": "base\n"}})
	f.commitInRepo(t, "app", map[string]string{"c.txt": "mine\n"}, "feature")
	return f
}

// TestWholePlanRunsPastFirstFailure: the plan never stops at the first
// failure — every command runs and is recorded, in order.
func TestWholePlanRunsPastFirstFailure(t *testing.T) {
	f := touched(t)
	eng := fakeEngine(t, map[string]any{"app": report("full",
		cmdOf("lint", "fast", "echo lint broke; exit 3"),
		cmdOf("unit", "full", "echo all good"))})
	res := runFixture(t, f, eng, nil)
	if c := checkOf(t, res, "app:lint"); c.Status != "fail" || !strings.Contains(c.Detail, "exit 3") {
		t.Errorf("lint = %+v", c)
	}
	if c := checkOf(t, res, "app:unit"); c.Status != "pass" {
		t.Errorf("the second command must still run: %+v", c)
	}
	if res.MechanicalVerdict != "fail" || !strings.Contains(res.FailReason, "app:lint") {
		t.Errorf("verdict %q / fail_reason %q", res.MechanicalVerdict, res.FailReason)
	}
	want := []TestBarEntry{{"app", "echo lint broke; exit 3"}, {"app", "echo all good"}}
	if len(res.TestBar) != 2 || res.TestBar[0] != want[0] || res.TestBar[1] != want[1] {
		t.Errorf("test_bar = %+v", res.TestBar)
	}
}

// TestUnmetPreconditionSkipped: a declared requires_env / requires_cmd
// that does not hold skips the command with the missing names; it never
// runs and is no part of the bar.
func TestUnmetPreconditionSkipped(t *testing.T) {
	f := touched(t)
	c := cmdOf("browser", "full", "touch ran.txt")
	c.RequiresEnv = []string{"VR_NO_SUCH_VAR"}
	c.RequiresCmd = []string{"vr-no-such-binary"}
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": report("full", c)}), nil)
	ck := checkOf(t, res, "app:browser")
	if ck.Status != "skipped" || !strings.Contains(ck.Detail, "env VR_NO_SUCH_VAR") || !strings.Contains(ck.Detail, "command vr-no-such-binary") {
		t.Errorf("check = %+v", ck)
	}
	if _, err := os.Stat(filepath.Join(f.repo("app"), "ran.txt")); err == nil {
		t.Error("a skipped command must not run")
	}
	if len(res.TestBar) != 0 {
		t.Errorf("test_bar = %v", res.TestBar)
	}
}

// TestFullTierAsksForTheFullPlan: on the full tier the engine is asked
// with --full and its full plan runs, never a scoped selection; the scoped
// tier never passes --full.
func TestFullTierAsksForTheFullPlan(t *testing.T) {
	f := touched(t)
	eng := fakeEngine(t, map[string]any{
		"app":      report("scoped", cmdOf("fast-1", "fast", "true"), cmdOf("scoped-1", "scoped", "echo scoped")),
		"app.full": report("full", cmdOf("fast-1", "fast", "true"), cmdOf("full-1", "full", "echo full")),
	})
	res := runFixture(t, f, eng, nil)
	var cmds []string
	for _, c := range res.Repos[0].Commands {
		cmds = append(cmds, c.Name)
	}
	if strings.Join(cmds, ",") != "fast-1,full-1" || res.Tier.Effective != TierFull {
		t.Errorf("ran %v (tier %+v), want the full plan", cmds, res.Tier)
	}
	args, _ := os.ReadFile(filepath.Join(filepath.Dir(eng), "args.log"))
	if !strings.Contains(string(args), "app --json --full --base ") {
		t.Errorf("engine args %q, want --json --full --base", args)
	}
	res = runFixture(t, f, eng, func(o *Options) { o.Tier = TierScoped })
	if c := res.Repos[0].Commands; len(c) != 2 || c[1].Name != "scoped-1" {
		t.Errorf("scoped tier ran %+v", c)
	}
	args, _ = os.ReadFile(filepath.Join(filepath.Dir(eng), "args.log"))
	if lines := strings.Split(strings.TrimSpace(string(args)), "\n"); strings.Contains(lines[len(lines)-1], "--full") {
		t.Errorf("the scoped tier passed --full: %q", lines[len(lines)-1])
	}
}

// TestFullTierWithoutFullPlanIsUnplanned: an engine answering --full with
// a scoped report gives no full plan: the repo is unplanned and its bar
// stays the step's.
func TestFullTierWithoutFullPlanIsUnplanned(t *testing.T) {
	f := touched(t)
	eng := fakeEngine(t, map[string]any{"app": report("scoped", cmdOf("scoped-1", "scoped", "echo scoped"))})
	res := runFixture(t, f, eng, nil)
	if len(res.UnplannedRepos) != 1 || res.UnplannedRepos[0] != "app" {
		t.Fatalf("unplanned = %v", res.UnplannedRepos)
	}
	if c := checkOf(t, res, "app:plan"); c.Status != "skipped" || !strings.Contains(c.Detail, "derives and runs") {
		t.Errorf("plan check = %+v", c)
	}
	if res.Repos[0].Plan != PlanNoFullPlan {
		t.Errorf("plan = %q", res.Repos[0].Plan)
	}
}

// TestScopedTierRunsScopedPlan: the scoped tier runs the fast gates plus
// the scoped selection and proves an effective scoped tier.
func TestScopedTierRunsScopedPlan(t *testing.T) {
	f := touched(t)
	eng := fakeEngine(t, map[string]any{"app": report("scoped", cmdOf("fast-1", "fast", "true"), cmdOf("scoped-1", "scoped", "echo s"))})
	res := runFixture(t, f, eng, func(o *Options) { o.Tier = TierScoped })
	if len(res.Repos[0].Commands) != 2 || res.Tier.Effective != TierScoped || res.Tier.Requested != TierScoped {
		t.Errorf("commands %+v, tier %+v", res.Repos[0].Commands, res.Tier)
	}
	if !strings.Contains(res.Scope.FastGatesRef, eng+" --json") || !strings.Contains(res.Scope.FastGatesRef, "conf present") {
		t.Errorf("fast_gates_ref = %q", res.Scope.FastGatesRef)
	}
}

// TestEngineFailureIsUnplannedAndFull: a failing engine leaves the repo
// unplanned, and a scoped request can no longer be an effective scoped run.
func TestEngineFailureIsUnplannedAndFull(t *testing.T) {
	f := touched(t)
	eng := fakeEngine(t, map[string]any{}) // no report: exits 3
	res := runFixture(t, f, eng, func(o *Options) { o.Tier = TierScoped })
	if res.Repos[0].Plan != PlanEngineFailed || res.Tier.Effective != TierFull {
		t.Errorf("plan %q, tier %+v", res.Repos[0].Plan, res.Tier)
	}
	if len(res.UnplannedRepos) != 1 {
		t.Errorf("unplanned = %v", res.UnplannedRepos)
	}
}

// TestConfChangedIsVisible: a conf changed in the diff is a visible
// skipped hint check and a result fact.
func TestConfChangedIsVisible(t *testing.T) {
	f := touched(t)
	rep := report("full", cmdOf("full-1", "full", "true"))
	rep["conf_changed_in_diff"] = true
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": rep}), nil)
	if c := checkOf(t, res, "app:conf-changed"); c.Status != "skipped" {
		t.Errorf("conf-changed = %+v", c)
	}
	if !res.ConfChanged["app"] {
		t.Errorf("conf_changed_in_diff = %v", res.ConfChanged)
	}
}

// TestLogsStayOutOfArtifactsAndTheChain: logs land under
// runtime-state/verify/<n>/<repo>/<k>.log behind a .gitignore that keeps
// the tree out of the capsule chain; nothing lands under artifacts/; the
// evidence has the envelope's shape.
func TestLogsStayOutOfArtifactsAndTheChain(t *testing.T) {
	f := touched(t)
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": report("full", cmdOf("full-1", "full", "echo hi"))}), nil)
	raw, err := os.ReadFile(filepath.Join(f.capsule, "runtime-state", "verify", ".gitignore"))
	if err != nil || !strings.Contains(string(raw), "\n*\n") {
		t.Fatalf(".gitignore = %q (%v)", raw, err)
	}
	if res.Run != 1 || res.OutDir != "runtime-state/verify/1" {
		t.Errorf("run %d out %q", res.Run, res.OutDir)
	}
	if _, err := os.Stat(filepath.Join(f.capsule, "artifacts")); err == nil {
		t.Error("verify-run must write nothing under artifacts/")
	}
	var bar *Evidence
	for i, e := range res.CommandEvidence {
		if !strings.HasPrefix(e.LogPath, "runtime-state/verify/1/app/") || !strings.HasSuffix(e.LogPath, ".log") {
			t.Errorf("evidence log %q", e.LogPath)
		}
		if e.DurationMs < 1 || e.StartedAt.IsZero() || e.Cwd != "repos/app" {
			t.Errorf("evidence %+v", e)
		}
		if strings.Join(e.Argv, " ") == "bash -c echo hi" {
			bar = &res.CommandEvidence[i]
		}
	}
	if bar == nil {
		t.Fatal("no bash -c evidence for the bar command")
	}
	// a second run is run 2, and git sees nothing to add
	res2 := runFixture(t, f, fakeEngine(t, map[string]any{"app": report("full", cmdOf("full-1", "full", "echo hi"))}), nil)
	if res2.Run != 2 {
		t.Errorf("second run = %d", res2.Run)
	}
	gitT(t, f.capsule, "init", "-q")
	if st := gitT(t, f.capsule, "status", "--porcelain", "--untracked-files=all", "--", "runtime-state"); st != "" {
		t.Errorf("runtime-state/verify is visible to the capsule chain:\n%s", st)
	}
}

// TestTimeoutKillsTheCommand: a command over its declared timeout is
// killed (its process group with it) and fails.
func TestTimeoutKillsTheCommand(t *testing.T) {
	f := touched(t)
	c := cmdOf("slow", "full", "(while :; do :; done) & wait")
	c.Timeout = "300ms"
	start := time.Now()
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": report("full", c)}), nil)
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("took %s", d)
	}
	ck := checkOf(t, res, "app:slow")
	if ck.Status != "fail" || !strings.Contains(ck.Detail, "timeout") || res.Repos[0].Commands[0].ExitCode != timeoutExit {
		t.Errorf("check %+v, command %+v", ck, res.Repos[0].Commands[0])
	}
}

// TestPrivateTmpdirAndKnobs: every command gets its own TMPDIR (removed
// afterwards) and the explicit resource knobs.
func TestPrivateTmpdirAndKnobs(t *testing.T) {
	f := touched(t)
	out := filepath.Join(t.TempDir(), "env.txt")
	cmd := `echo "$TMPDIR $E2E_JOBS $GOMAXPROCS" >> ` + out
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": report("full", cmdOf("a", "full", cmd), cmdOf("b", "full", cmd))}),
		func(o *Options) { o.E2EJobs, o.GoMaxProcs = 2, 3 })
	if res.MechanicalVerdict != "pass" {
		t.Fatalf("verdict %s: %s", res.MechanicalVerdict, res.FailReason)
	}
	raw, _ := os.ReadFile(out)
	ls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(ls) != 2 {
		t.Fatalf("env lines %q", raw)
	}
	a, b := strings.Fields(ls[0]), strings.Fields(ls[1])
	if a[0] == b[0] || a[0] == os.Getenv("TMPDIR") || a[1] != "2" || a[2] != "3" {
		t.Errorf("env = %q", raw)
	}
	if _, err := os.Stat(a[0]); err == nil {
		t.Errorf("private TMPDIR %s survived", a[0])
	}
}

// TestUntrackedTestFilesAndTrackedTestFiles: an untracked test file by
// the generic patterns or the conf's test_glob fails the check; declared
// test_files must be tracked where they exist and exist somewhere.
func TestUntrackedTestFilesAndTrackedTestFiles(t *testing.T) {
	f := touched(t)
	f.commitInRepo(t, "app", map[string]string{"test/accept_test.go": "package test\n"}, "authored")
	f.execution(t, map[string]map[string]any{"e2e-author": {"scenario_titles": []any{}, "test_files": []any{"test/accept_test.go", "test/gone_test.go"}}})
	writeFile(t, f.repo("app"), "pkg/stray_test.go", "package pkg\n")
	writeFile(t, f.repo("app"), "checks/x.check", "x\n")
	writeFile(t, f.repo("app"), "notes.txt", "not a test\n")
	rep := report("full", cmdOf("full-1", "full", "true"))
	rep["test_glob"] = []string{"*.check"}
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": rep}), nil)
	c := checkOf(t, res, "app:no-untracked-test-files")
	if c.Status != "fail" || !strings.Contains(c.Detail, "pkg/stray_test.go") || !strings.Contains(c.Detail, "checks/x.check") || strings.Contains(c.Detail, "notes.txt") {
		t.Errorf("untracked check = %+v", c)
	}
	if p := checkOf(t, res, "test-files-present"); p.Status != "fail" || !strings.Contains(p.Detail, "test/gone_test.go") {
		t.Errorf("present check = %+v", p)
	}
	for _, p := range []string{"pkg/stray_test.go", "checks/x.check"} {
		if err := os.Remove(filepath.Join(f.repo("app"), p)); err != nil {
			t.Fatal(err)
		}
	}
	f.execution(t, map[string]map[string]any{"e2e-author": {"scenario_titles": []any{}, "test_files": []any{"test/accept_test.go"}}})
	res = runFixture(t, f, fakeEngine(t, map[string]any{"app": rep}), nil)
	if c := checkOf(t, res, "app:no-untracked-test-files"); c.Status != "pass" || !strings.Contains(c.Detail, "test/accept_test.go") {
		t.Errorf("clean check = %+v", c)
	}
}

// TestAuthoredTestsUnchanged: a branch commit touching an authored test
// after its stamp fails the advisory check; merges of the default branch
// do not count.
func TestAuthoredTestsUnchanged(t *testing.T) {
	f := touched(t)
	stamp := f.commitInRepo(t, "app", map[string]string{"test/accept_test.go": "package test\n"}, "authored")
	f.execution(t, map[string]map[string]any{"e2e-author": {"scenario_titles": []any{}, "test_files": []any{"test/accept_test.go"},
		"test_files_commit": map[string]any{"app": stamp}}})
	eng := fakeEngine(t, map[string]any{"app": report("full", cmdOf("full-1", "full", "true"))})
	if c := checkOf(t, runFixture(t, f, eng, nil), "app:authored-tests-unchanged"); c.Status != "pass" {
		t.Errorf("unchanged = %+v", c)
	}
	f.commitInRepo(t, "app", map[string]string{"test/accept_test.go": "package test\n// edited\n"}, "edit")
	if c := checkOf(t, runFixture(t, f, eng, nil), "app:authored-tests-unchanged"); c.Status != "fail" {
		t.Errorf("edited = %+v", c)
	}
}

// TestRequiresCmdPathResolvesInTheRepo: a required command naming a path
// resolves against the repo the command runs in, a bare name on PATH.
func TestRequiresCmdPathResolvesInTheRepo(t *testing.T) {
	f := touched(t)
	writeFile(t, f.repo("app"), "bin/tool", "#!/bin/sh\necho tool ran\n")
	if err := os.Chmod(filepath.Join(f.repo("app"), "bin/tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.repo("app"), "add", "-A")
	gitT(t, f.repo("app"), "commit", "-q", "-m", "tool")
	ok := cmdOf("tool", "full", "./bin/tool")
	ok.RequiresCmd = []string{"./bin/tool", "sh"}
	missing := cmdOf("other", "full", "./bin/none")
	missing.RequiresCmd = []string{"./bin/none"}
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": report("full", ok, missing)}), nil)
	if c := checkOf(t, res, "app:tool"); c.Status != "pass" {
		t.Errorf("a repo-relative required command present in the repo must run: %+v", c)
	}
	if c := checkOf(t, res, "app:other"); c.Status != "skipped" || !strings.Contains(c.Detail, "command ./bin/none") {
		t.Errorf("a missing repo-relative command is skipped naming it: %+v", c)
	}
}

// TestDeadlineBoundsTheWholeRun: no command runs past the run's deadline —
// a running one is killed at it, a later one never starts — both fail,
// and the result is still written in time.
func TestDeadlineBoundsTheWholeRun(t *testing.T) {
	f := touched(t)
	eng := fakeEngine(t, map[string]any{"app": report("full", cmdOf("slow", "full", "sleep 60"), cmdOf("later", "full", "true"))})
	start := time.Now()
	res := runFixture(t, f, eng, func(o *Options) { o.Deadline = writeMargin + 3*time.Second })
	if d := time.Since(start); d > writeMargin+3*time.Second {
		t.Fatalf("the run took %s, past its deadline", d)
	}
	if c := checkOf(t, res, "app:slow"); c.Status != "fail" || !strings.Contains(c.Detail, "deadline") {
		t.Errorf("slow = %+v", c)
	}
	if c := checkOf(t, res, "app:later"); c.Status != "fail" || !strings.Contains(c.Detail, "not started") {
		t.Errorf("later = %+v", c)
	}
	if len(res.TestBar) != 1 || res.TestBar[0].Command != "sleep 60" {
		t.Errorf("test_bar = %+v (a command that never started is no bar entry)", res.TestBar)
	}
	if _, err := os.Stat(filepath.Join(f.capsule, res.OutDir, "result.json")); err != nil {
		t.Error(err)
	}
}

func TestDefaultDeadlineFor(t *testing.T) {
	for env, want := range map[string]time.Duration{
		"":                            DefaultDeadline,
		"BASH_MAX_TIMEOUT_MS=1740000": 27 * time.Minute,
		"BASH_MAX_TIMEOUT_MS=60000":   time.Minute,
		"BASH_MAX_TIMEOUT_MS=bogus":   DefaultDeadline,
	} {
		if got := DefaultDeadlineFor([]string{"PATH=/bin", env}); got != want {
			t.Errorf("%q: %s, want %s", env, got, want)
		}
	}
}

// TestEngineDiagnosticsSurface: the engine's diagnostics reach the result
// per repo and as a visible skipped check; the conf-changed hint names who
// sees it.
func TestEngineDiagnosticsSurface(t *testing.T) {
	f := touched(t)
	rep := report("full", cmdOf("full-1", "full", "true"))
	rep["diagnostics"] = []string{"verify-scope: unknown key foo= ignored"}
	rep["conf_changed_in_diff"] = true
	res := runFixture(t, f, fakeEngine(t, map[string]any{"app": rep}), nil)
	if c := checkOf(t, res, "app:engine-diagnostics"); c.Status != "skipped" || !strings.Contains(c.Detail, "unknown key foo=") {
		t.Errorf("diagnostics check = %+v", c)
	}
	if d := res.Repos[0].Diagnostics; len(d) != 1 {
		t.Errorf("repo diagnostics = %v", d)
	}
	if c := checkOf(t, res, "app:conf-changed"); !strings.Contains(c.Detail, "deliver and the owner see it") {
		t.Errorf("conf-changed = %+v", c)
	}
	raw, _ := os.ReadFile(filepath.Join(f.capsule, res.OutDir, "report-mechanical.md"))
	if !strings.Contains(string(raw), "unknown key foo=") {
		t.Errorf("the report must carry the engine diagnostics:\n%s", raw)
	}
}
