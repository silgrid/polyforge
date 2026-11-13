package stepscripts

// facts_test.go: the workflow's facts.py (it took over pf-facts, run as a
// step runs it) — each kind on a fixture capsule: the plan with and
// without conformance (and the units' test files counted as declared), the
// units with test-first commits and without, the red run replayed at the
// test commit in the verification container of a fake docker (red, not
// red, a cwd inside the repo, no run, no engine) and the worktree gone afterwards,
// the carries declaration matching and missing, the micro bounds in and
// out with a conf declaring micro_path; the result's heads, round and
// exit codes.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeUnits writes units.json under the capsule and answers its path.
func writeUnits(t *testing.T, f *capsuleFixture, units []factsUnitDecl) string {
	t.Helper()
	raw, err := json.Marshal(units)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.dir, "artifacts/build/units.json", string(raw))
	return filepath.Join(f.dir, "artifacts", "build", "units.json")
}

// factsRun runs facts.py with the kind and args on the capsule, the
// result written under artifacts/facts/<kind>.json; it answers the exit,
// the result and stderr.
func factsRun(t *testing.T, f *capsuleFixture, rt *fakeRuntime, kind string, args ...string) (int, *Result, string) {
	t.Helper()
	out := filepath.Join(f.dir, "artifacts", "facts", kind+".json")
	argv := append([]string{kind, "--capsule", f.dir, "--json", "--out", out}, args...)
	exit, stdout, errb := invoke(t, NameFacts, argv, stepEnv("PF_ROUND=r7", "PF_TOOL_WINDOW_MS=1800000"), rt)
	if exit == 2 && stdout == "" {
		return exit, nil, errb
	}
	res := readResult(t, out)
	var printed Result
	if err := json.Unmarshal([]byte(stdout), &printed); err != nil || printed.Command != NameFacts {
		t.Fatalf("stdout is not the result: %v\n%s", err, stdout)
	}
	return exit, res, errb
}

func TestFactsNeedsAKind(t *testing.T) {
	for _, args := range [][]string{nil, {"--json"}, {"nonsense"}} {
		exit, _, errb := invoke(t, NameFacts, args, stepEnv(), newFakeRuntime(t))
		if exit != 2 || !strings.Contains(errb, "plan-conformance | build | carries | micro-bounds") {
			t.Fatalf("args %v: exit %d, stderr %q", args, exit, errb)
		}
	}
	f := newCapsule(t, map[string]string{"a.go": "a\n"})
	exit, _, errb := invoke(t, NameFacts, []string{FactsMicroBounds, "--capsule", f.dir}, stepEnv(), newFakeRuntime(t))
	if exit != 2 || !strings.Contains(errb, "--plan <file> is required") {
		t.Fatalf("micro-bounds without a plan: exit %d, stderr %q", exit, errb)
	}
	exit, _, errb = invoke(t, NameFacts, []string{FactsCarries, "--capsule", t.TempDir()}, stepEnv(), newFakeRuntime(t))
	if exit != 2 || !strings.Contains(errb, "not a capsule root") {
		t.Fatalf("no capsule: exit %d, stderr %q", exit, errb)
	}
}

const conformantPlan = "# Plan\n\n## Units\n\n1. **u1** — a\n   - Touched files: `a.go` (write), `x.go` (write)\n   - Coupled obligations: none — nothing lists them\n"
const obligedPlan = "# Plan\n\n## Units\n\n1. **u1** — a\n   - Touched files: `a.go` (write)\n   - Coupled obligations: `docs/a.md` (the row)\n"

func TestFactsPlanConformanceBothWays(t *testing.T) {
	f := newCapsule(t, map[string]string{"a.go": "a\n", "docs/a.md": "doc\n"})
	f.change(t, map[string]string{"a.go": "a2\n", "x.go": "x\n"})
	writeFile(t, f.dir, "artifacts/plan.md", conformantPlan)
	exit, res, errb := factsRun(t, f, newFakeRuntime(t), FactsPlanConformance)
	if exit != 0 || res.Verdict != VerdictPass || res.Round != "r7" || res.Heads["app"] != f.head(t) {
		t.Fatalf("conformant plan: exit %d %+v\n%s", exit, res, errb)
	}
	var d planConformanceDetails
	detailsOf(t, res, &d)
	if d.Plan != "artifacts/plan.md" || d.UnitsFile != "" || len(d.Obligations) != 2 || len(d.Undeclared) != 0 || d.Obligations[0].NoneReason != "nothing lists them" {
		t.Fatalf("details: %+v", d)
	}
	if raw, _ := json.Marshal(res.Details); !strings.Contains(string(raw), `"undeclared":[]`) || !strings.Contains(string(raw), `"released":[]`) {
		t.Fatalf("the lists are never null: %s", raw)
	}

	writeFile(t, f.dir, "artifacts/plan.md", obligedPlan)
	exit, res, _ = factsRun(t, f, newFakeRuntime(t), FactsPlanConformance)
	detailsOf(t, res, &d)
	if exit != 1 || res.Verdict != VerdictFail || strings.Join(d.Undeclared, ",") != "x.go" || strings.Join(d.UnchangedObligations, ",") != "docs/a.md" {
		t.Fatalf("a plan the diff departs from: exit %d %+v (%s)", exit, d, res.Summary)
	}
	if !strings.Contains(res.Summary, "1 path(s) changed but undeclared, 1 declared coupled obligation(s) left unchanged") {
		t.Fatalf("summary: %s", res.Summary)
	}

	// the units' test files are declared by the units: never undeclared
	f.change(t, map[string]string{"a_test.go": "t\n"})
	tc := f.head(t)
	writeUnits(t, f, []factsUnitDecl{{ID: "u1", TestCommit: tc, ImplCommit: tc}})
	exit, res, _ = factsRun(t, f, newFakeRuntime(t), FactsPlanConformance)
	detailsOf(t, res, &d)
	if exit != 1 || d.UnitsFile != "artifacts/build/units.json" || strings.Join(d.Undeclared, ",") != "x.go" {
		t.Fatalf("an authored test file counts as declared: exit %d %+v", exit, d)
	}

	// an unreadable plan is not judged
	exit, res, _ = factsRun(t, f, newFakeRuntime(t), FactsPlanConformance, "--plan", filepath.Join(f.dir, "artifacts", "none.md"))
	detailsOf(t, res, &d)
	if exit != 2 || res.Verdict != VerdictError || !strings.Contains(d.Unavailable, "unreadable") || !strings.HasPrefix(res.Summary, "not judged: ") {
		t.Fatalf("no plan: exit %d %+v (%s)", exit, d, res.Summary)
	}
}

// testFirst commits a unit red-then-green on the fixture: the test file
// alone, then the implementation; it answers the two commits.
func testFirst(t *testing.T, f *capsuleFixture, id string) (tc, ic string) {
	t.Helper()
	tc = f.change(t, map[string]string{id + "_test.go": "package app\n"})
	ic = f.change(t, map[string]string{id + ".go": "package app\n"})
	return tc, ic
}

func TestFactsBuildUnitsAndCommits(t *testing.T) {
	f := newCapsule(t, map[string]string{"app.go": "package app\n"})
	exit, _, errb := factsRun(t, f, newFakeRuntime(t), FactsBuild)
	if exit != 2 || !strings.Contains(errb, "--units") {
		t.Fatalf("no units.json: exit %d %q", exit, errb)
	}
	writeUnits(t, f, nil)
	exit, res, _ := factsRun(t, f, newFakeRuntime(t), FactsBuild)
	if exit != 1 || !strings.Contains(res.Summary, "no unit is reported") {
		t.Fatalf("an empty list proves nothing: exit %d %s", exit, res.Summary)
	}

	tc, ic := testFirst(t, f, "u1")
	mixed := f.change(t, map[string]string{"u2_test.go": "package app\n", "u2.go": "package app\n"})
	writeUnits(t, f, []factsUnitDecl{{ID: "u1", TestCommit: tc, ImplCommit: ic, Run: &factsUnitRun{Repo: "app", Argv: []string{"go", "test", "-run", "U1"}}}})
	exit, res, _ = factsRun(t, f, newFakeRuntime(t), FactsBuild)
	var d buildDetails
	detailsOf(t, res, &d)
	if exit != 0 || res.Verdict != VerdictPass || len(d.Units) != 1 || d.Units[0].Commits != "" || d.Units[0].Repo != "app" || strings.Join(d.Units[0].TestFiles, ",") != "u1_test.go" || d.Units[0].Run == nil || d.Replay != nil || len(d.TestWeakened) != 0 {
		t.Fatalf("a test-first unit: exit %d %+v (%s)", exit, d, res.Summary)
	}

	// a test file changed after its red commit is a fact, not a verdict
	f.change(t, map[string]string{"u1_test.go": "package app\n\n// relaxed\n"})
	exit, res, _ = factsRun(t, f, newFakeRuntime(t), FactsBuild)
	detailsOf(t, res, &d)
	if exit != 0 || len(d.TestWeakened) != 0 {
		t.Fatalf("a pure addition is no weakening: exit %d %+v", exit, d.TestWeakened)
	}
	f.change(t, map[string]string{"u1_test.go": "package other\n"})
	exit, res, _ = factsRun(t, f, newFakeRuntime(t), FactsBuild)
	detailsOf(t, res, &d)
	if exit != 0 || len(d.TestWeakened) != 1 || d.TestWeakened[0].Unit != "u1" || !strings.Contains(d.TestWeakened[0].Reason, "deletes or changes") || !strings.Contains(res.Summary, "1 test_weakened hunk(s)") {
		t.Fatalf("a changed test line is a test_weakened fact and moves no exit code: exit %d %+v (%s)", exit, d.TestWeakened, res.Summary)
	}

	// a pair that is not test-first, and a unit that does not resolve
	writeUnits(t, f, []factsUnitDecl{
		{ID: "u1", TestCommit: tc, ImplCommit: ic},
		{ID: "u2", TestCommit: mixed, ImplCommit: f.head(t)},
		{ID: "u3", TestCommit: "deadbeef1", ImplCommit: ic},
	})
	exit, res, _ = factsRun(t, f, newFakeRuntime(t), FactsBuild)
	detailsOf(t, res, &d)
	if exit != 1 || res.Verdict != VerdictFail || len(d.Units) != 3 || !strings.Contains(d.Units[1].Commits, "touches non-test paths u2.go") || d.Units[2].Why == "" {
		t.Fatalf("units: exit %d %+v", exit, d.Units)
	}
	if !strings.Contains(res.Summary, "2 of 3 unit(s) fail the test-first check") {
		t.Fatalf("summary: %s", res.Summary)
	}
}

// replayCapsule is a capsule whose repo carries the Dockerfile and one
// test-first unit whose run `sh test/run.sh` is red before impl.txt
// exists (the test commit) and green after (the implementation commit).
func replayCapsule(t *testing.T) (f *capsuleFixture, tc, ic string) {
	t.Helper()
	f = newCapsule(t, map[string]string{"a.txt": "a\n", DockerfileVerify: testDockerfile})
	tc = f.change(t, map[string]string{"test/run.sh": "test -f impl.txt\n", "sub/test/go.sh": "test -f ../impl.txt\n"})
	ic = f.change(t, map[string]string{"impl.txt": "done\n", "sub/impl.txt": "done\n"})
	return f, tc, ic
}

// replayWorktrees answers the clone's worktrees beyond itself.
func replayWorktrees(t *testing.T, f *capsuleFixture) []string {
	t.Helper()
	var extra []string
	for _, l := range strings.Split(gitT(t, f.repo(), "worktree", "list", "--porcelain"), "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok && p != f.repo() {
			extra = append(extra, p)
		}
	}
	return extra
}

func TestFactsBuildReplayRedAtTheTestCommit(t *testing.T) {
	f, tc, ic := replayCapsule(t)
	run := &factsUnitRun{Repo: "app", Cwd: ".", Argv: []string{"sh", "test/run.sh"}}
	sub := &factsUnitRun{Repo: "app", Cwd: "sub", Argv: []string{"sh", "test/go.sh"}}
	writeUnits(t, f, []factsUnitDecl{{ID: "u1", TestCommit: tc, ImplCommit: ic, Run: run}, {ID: "u 2/x", TestCommit: tc, ImplCommit: ic, Run: sub}})
	rt := newFakeRuntime(t)
	exit, res, errb := factsRun(t, f, rt, FactsBuild, "--replay", "--unit-timeout", "2m")
	if exit != 0 || res.Verdict != VerdictPass {
		t.Fatalf("exit %d %s\n%s", exit, res.Summary, errb)
	}
	var d buildDetails
	detailsOf(t, res, &d)
	if d.Replay == nil || d.Replay.UnitTimeout != "2m0s" || len(d.Units) != 2 {
		t.Fatalf("details: %+v", d)
	}
	for i, u := range d.Units {
		r := u.Replay
		if r == nil || !r.Red || r.Exit != 1 || r.TimedOut || u.ReplayError != "" {
			t.Fatalf("unit %d replay: %+v (%s)", i, r, u.ReplayError)
		}
		if !strings.HasPrefix(r.Worktree, ".git/pf-replay/") || r.Line != strings.Join(u.Run.Argv, " ") {
			t.Fatalf("unit %d ran %q in %q", i, r.Line, r.Worktree)
		}
		if r.Log == "" || !strings.HasPrefix(r.Log, "artifacts/facts/logs/replay/") {
			t.Fatalf("unit %d log: %q", i, r.Log)
		}
		if _, err := os.Stat(filepath.Join(f.dir, filepath.FromSlash(r.Log))); err != nil {
			t.Fatalf("unit %d log file: %v", i, err)
		}
	}
	if d.Units[1].Replay.Cwd != "sub" {
		t.Fatalf("the run's cwd inside the repo: %+v", d.Units[1].Replay)
	}
	if len(res.Logs) != 2 || res.Logs[0] != d.Units[0].Replay.Log {
		t.Fatalf("logs: %v", res.Logs)
	}
	if !strings.Contains(res.Summary, "replay red at their test commits") {
		t.Fatalf("summary: %s", res.Summary)
	}
	// the clone is as it was: no worktree left, HEAD and status untouched
	if extra := replayWorktrees(t, f); len(extra) != 0 {
		t.Fatalf("worktrees left behind: %v", extra)
	}
	if f.head(t) != ic || gitT(t, f.repo(), "status", "--porcelain") != "" {
		t.Fatalf("the clone moved: HEAD %s status %q", f.head(t), gitT(t, f.repo(), "status", "--porcelain"))
	}
	// every container the replays started was removed
	if len(rt.starts) != 2 || len(rt.removes) != 2 {
		t.Fatalf("containers started %d removed %d", len(rt.starts), len(rt.removes))
	}
	for _, s := range rt.starts {
		if s.Binds[0].Path != f.repo() || s.Labels[LabelExpires] == "" {
			t.Fatalf("container spec: %+v", s)
		}
	}
}

func TestFactsBuildReplayNotRedFailsAndNoRunIsAProblem(t *testing.T) {
	f, tc, ic := replayCapsule(t)
	writeUnits(t, f, []factsUnitDecl{
		{ID: "u1", TestCommit: tc, ImplCommit: ic, Run: &factsUnitRun{Repo: "app", Argv: []string{"true"}}},
		{ID: "u2", TestCommit: tc, ImplCommit: ic},
		{ID: "u3", TestCommit: tc, ImplCommit: ic, Run: &factsUnitRun{Repo: "other", Argv: []string{"true"}}},
	})
	exit, res, _ := factsRun(t, f, newFakeRuntime(t), FactsBuild, "--replay")
	var d buildDetails
	detailsOf(t, res, &d)
	if exit != 1 || res.Verdict != VerdictFail {
		t.Fatalf("exit %d %s", exit, res.Summary)
	}
	if r := d.Units[0].Replay; r == nil || r.Red || r.Exit != 0 {
		t.Fatalf("u1 passes before its implementation: %+v", r)
	}
	if d.Units[1].Replay != nil || d.Units[2].Replay != nil {
		t.Fatalf("a unit without a usable run is not replayed: %+v %+v", d.Units[1], d.Units[2])
	}
	for _, want := range []string{"3 of 3 unit(s) fail", "exited 0 on the tree of test_commit", "reports no run", "its run names repos/other"} {
		if !strings.Contains(res.Summary, want) {
			t.Fatalf("summary lacks %q: %s", want, res.Summary)
		}
	}
	if extra := replayWorktrees(t, f); len(extra) != 0 {
		t.Fatalf("worktrees left behind: %v", extra)
	}
}

func TestFactsBuildReplayWithoutAnEngineIsNotJudged(t *testing.T) {
	f, tc, ic := replayCapsule(t)
	writeUnits(t, f, []factsUnitDecl{{ID: "u1", TestCommit: tc, ImplCommit: ic, Run: &factsUnitRun{Repo: "app", Argv: []string{"sh", "test/run.sh"}}}})
	rt := newFakeRuntime(t)
	rt.unavailable = errors.New("no docker here")
	exit, res, _ := factsRun(t, f, rt, FactsBuild, "--replay")
	var d buildDetails
	detailsOf(t, res, &d)
	if exit != 2 || res.Verdict != VerdictError || d.Units[0].ReplayError == "" || !strings.Contains(res.Summary, "could not be replayed") {
		t.Fatalf("exit %d %s %+v", exit, res.Summary, d.Units[0])
	}
	if extra := replayWorktrees(t, f); len(extra) != 0 {
		t.Fatalf("worktrees left behind: %v", extra)
	}
	// without --replay the same units pass on their commits alone
	if exit, res, _ := factsRun(t, f, rt, FactsBuild); exit != 0 || res.Verdict != VerdictPass {
		t.Fatalf("no replay asked: exit %d %s", exit, res.Summary)
	}
}

// A replayed run that could not run at all at its test commit — exit 127
// (the command is not found) or 126 (found, not executable) — is not
// judged (exit 2): the test-first evidence is unknown, never red (red
// before: such an exit counted as the red the check wants, and passed).
func TestFactsBuildReplayThatCannotRunIsNotJudged(t *testing.T) {
	for _, code := range []int{127, 126} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			f, tc, ic := replayCapsule(t)
			writeUnits(t, f, []factsUnitDecl{{ID: "u1", TestCommit: tc, ImplCommit: ic, Run: &factsUnitRun{Repo: "app", Argv: []string{"sh", "-c", fmt.Sprintf("exit %d", code)}}}})
			exit, res, _ := factsRun(t, f, newFakeRuntime(t), FactsBuild, "--replay")
			var d buildDetails
			detailsOf(t, res, &d)
			if exit != 2 || res.Verdict != VerdictError || !strings.Contains(res.Summary, "not judged") || !strings.Contains(res.Summary, fmt.Sprintf("exit %d", code)) {
				t.Fatalf("SYMPTOM: exit %d at the test commit was judged: exit %d %s %s", code, exit, res.Verdict, res.Summary)
			}
			if r := d.Units[0].Replay; r == nil || r.Red || r.Exit != code {
				t.Fatalf("the replay is recorded, never red: %+v", r)
			}
			if extra := replayWorktrees(t, f); len(extra) != 0 {
				t.Fatalf("worktrees left behind: %v", extra)
			}
		})
	}
}

// carriesSeed is a repo with a nested npm root `web/`.
var carriesSeed = map[string]string{
	"app.go":                           "package app\n",
	"web/.polyforge/verify-scope.conf": "stack=npm\n",
	"web/package.json":                 `{"scripts":{}}` + "\n",
	"web/lib/panel.ts":                 "export const panel = 1;\n",
}

func TestFactsCarriesDeclaredAndMissing(t *testing.T) {
	f := newCapsule(t, carriesSeed)
	f.change(t, map[string]string{"web/lib/panel.ts": "export const panel = 2;\n", "web/e2e/panel.mjs": "// carries: lib/panel*\nimport 'node:assert';\n"})
	exit, res, errb := factsRun(t, f, newFakeRuntime(t), FactsCarries)
	if exit != 0 || res.Verdict != VerdictPass || res.Heads["app"] != f.head(t) {
		t.Fatalf("a declared suite: exit %d %s\n%s", exit, res.Summary, errb)
	}
	var d carriesDetails
	detailsOf(t, res, &d)
	if len(d.Repos) != 1 || d.Repos[0].Name != "app" || d.Repos[0].UnitRepo || len(d.Repos[0].Problems) != 0 || len(d.Problems) != 0 {
		t.Fatalf("details: %+v", d)
	}

	// a suite the units' run names must match a touched path
	tc := f.change(t, map[string]string{"web/e2e/other.mjs": "// carries: lib/other*\n"})
	ic := f.change(t, map[string]string{"web/lib/panel.ts": "export const panel = 3;\n"})
	writeUnits(t, f, []factsUnitDecl{{ID: "u1", TestCommit: tc, ImplCommit: ic, Run: &factsUnitRun{Repo: "app", Cwd: "web", Argv: []string{"node", "e2e/other.mjs"}}}})
	exit, res, _ = factsRun(t, f, newFakeRuntime(t), FactsCarries)
	detailsOf(t, res, &d)
	if exit != 1 || res.Verdict != VerdictFail || !d.Repos[0].UnitRepo || d.UnitsFile != "artifacts/build/units.json" || len(d.Problems) != 1 || !strings.Contains(d.Problems[0], "matches none of the non-test paths") {
		t.Fatalf("a bound suite covering nothing touched: exit %d %+v", exit, d)
	}

	g := newCapsule(t, carriesSeed)
	g.change(t, map[string]string{"web/e2e/bare.mjs": "import 'node:assert';\n"})
	exit, res, _ = factsRun(t, g, newFakeRuntime(t), FactsCarries)
	detailsOf(t, res, &d)
	if exit != 1 || len(d.Problems) != 1 || !strings.Contains(d.Problems[0], "declares no `carries:`") || !strings.HasPrefix(res.Summary, "1 carries problem(s): ") {
		t.Fatalf("an undeclared suite: exit %d %+v (%s)", exit, d, res.Summary)
	}
}

func TestFactsMicroBoundsInAndOut(t *testing.T) {
	f := newCapsule(t, map[string]string{".polyforge/verify-scope.conf": "micro_path=docs/**\nmicro_check=sh keys.sh\n", "docs/a.md": "a\n", "src/x.go": "x\n"})
	f.change(t, map[string]string{"docs/a.md": "b\n"})
	writeFile(t, f.dir, "artifacts/fix/evidence.json", `[{"repo":"app","line":"sh fast.sh"}]`)
	plan := filepath.Join(f.dir, "artifacts", "fix", "evidence.json")
	exit, res, errb := factsRun(t, f, newFakeRuntime(t), FactsMicroBounds, "--plan", plan)
	if exit != 0 || res.Verdict != VerdictPass || res.Heads["app"] != f.head(t) {
		t.Fatalf("in bounds: exit %d %s\n%s", exit, res.Summary, errb)
	}
	var d microBoundsDetails
	detailsOf(t, res, &d)
	if !d.InBounds || d.Plan != "artifacts/fix/evidence.json" || len(d.Repos) != 1 || strings.Join(d.Repos[0].ChangedPaths, ",") != "docs/a.md" {
		t.Fatalf("details: %+v", d)
	}
	if raw, _ := json.Marshal(d.Repos[0].Commands); string(raw) != `[{"repo":"app","command":"sh fast.sh"},{"repo":"app","command":"sh keys.sh"}]` {
		t.Fatalf("the credential plan: %s", raw)
	}

	f.change(t, map[string]string{"src/x.go": "y\n"})
	exit, res, _ = factsRun(t, f, newFakeRuntime(t), FactsMicroBounds, "--plan", plan)
	detailsOf(t, res, &d)
	if exit != 1 || res.Verdict != VerdictFail || d.InBounds || !strings.Contains(d.Reason, "changed path(s) outside its declared micro_path") || !strings.Contains(d.Reason, "src/x.go") || res.Summary != d.Reason {
		t.Fatalf("out of bounds: exit %d %+v", exit, d)
	}
	if strings.Contains(d.Reason, "re-file") {
		t.Fatalf("the command names no lane to re-file on (the agent asks the owner): %s", d.Reason)
	}

	exit, _, errb = factsRun(t, f, newFakeRuntime(t), FactsMicroBounds, "--plan", filepath.Join(f.dir, "none.json"))
	if exit != 2 || !strings.Contains(errb, "--plan") {
		t.Fatalf("no plan file: exit %d %q", exit, errb)
	}
}
