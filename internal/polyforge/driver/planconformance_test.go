package driver

// planconformance_test.go: the two-way plan-conformance facts
// both set differences with their severities, the
// finding-cited extension gate, the unavailable-base note, the git diff
// read and the prompt section.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func jsonMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("fixture %s: %v", s, err)
	}
	return m
}

const conformancePlan = `{"touched_files":[
	{"path":"a.go","intent":"write","coupled_obligations":[{"path":"docs/a.md","note":"the a row"}]},
	{"path":"b.go","intent":"write","coupled_obligations":[{"none":true,"reason":"nothing lists b"}]},
	{"path":"c.go","intent":"write","coupled_obligations":[{"none":true,"reason":"fixture"}]},
	{"path":"r.go","intent":"read"}]}`

func TestPlanConformanceBothDirections(t *testing.T) {
	in := planConformanceInputs{plan: jsonMap(t, conformancePlan)}
	pc := foldPlanConformance(in, map[string][]string{"app": {"a.go", "b.go", "r.go", "x.go"}}, "")
	if want := []string{"r.go", "x.go"}; !reflect.DeepEqual(pc.Undeclared, want) {
		t.Fatalf("changed but undeclared = %v, want %v (a read declaration does not declare a write)", pc.Undeclared, want)
	}
	if want := []string{"docs/a.md"}; !reflect.DeepEqual(pc.UnchangedObligations, want) {
		t.Fatalf("unchanged obligations = %v, want %v", pc.UnchangedObligations, want)
	}
	if want := []string{"c.go"}; !reflect.DeepEqual(pc.UnchangedWrites, want) {
		t.Fatalf("unchanged plain writes = %v, want %v", pc.UnchangedWrites, want)
	}
	s := renderPlanConformance(pc)
	for needle, label := range map[string]string{
		"`x.go`":      "blocking: changed but undeclared",
		"`docs/a.md`": "blocking: declared coupled obligation left unchanged",
		"`c.go`":      "non-blocking: declared write left unchanged",
	} {
		if ctx := w78Line(s, needle); !strings.Contains(ctx, label) {
			t.Fatalf("%s must render with %q, got %q:\n%s", needle, label, ctx, s)
		}
	}
	if !strings.Contains(s, planConformanceHeading) || !strings.Contains(s, "`b.go` → none: nothing lists b") {
		t.Fatalf("section must carry the heading and the obligation mapping:\n%s", s)
	}
}

// w78Line answers the first line containing needle.
func w78Line(s, needle string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}

func TestPlanConformanceCitedExtension(t *testing.T) {
	build := jsonMap(t, `{"declared_extensions":[{"path":"x.go","finding_id":"s3#1"},{"path":"y.go","finding_id":"finding 2"},{"path":"z.go"}]}`)
	changed := map[string][]string{"app": {"a.go", "docs/a.md", "x.go", "y.go", "z.go"}}

	// no review→build rewind: no finding exists to cite.
	pc := foldPlanConformance(planConformanceInputs{plan: jsonMap(t, conformancePlan), build: build}, changed, "")
	if want := []string{"x.go", "y.go", "z.go"}; !reflect.DeepEqual(pc.Undeclared, want) || len(pc.Cited) != 0 {
		t.Fatalf("without a review rewind every extension stays undeclared: undeclared=%v cited=%v", pc.Undeclared, pc.Cited)
	}

	// after a review FAIL rewound to build: only the well-formed citation counts.
	pc = foldPlanConformance(planConformanceInputs{plan: jsonMap(t, conformancePlan), build: build, reviewRound: true}, changed, "")
	if want := []string{"y.go", "z.go"}; !reflect.DeepEqual(pc.Undeclared, want) {
		t.Fatalf("undeclared = %v, want %v (malformed and missing finding ids never count)", pc.Undeclared, want)
	}
	if want := []declaredExtension{{Path: "x.go", FindingID: "s3#1"}}; !reflect.DeepEqual(pc.Cited, want) {
		t.Fatalf("cited = %v, want %v", pc.Cited, want)
	}
	s := renderPlanConformance(pc)
	if l := w78Line(s, "`x.go`"); !strings.Contains(l, "s3#1") || strings.Contains(l, "undeclared") {
		t.Fatalf("the cited file must echo its finding and not read as undeclared, got %q", l)
	}
}

func TestPlanConformanceUnavailableBaseSaysSo(t *testing.T) {
	pc := foldPlanConformance(planConformanceInputs{plan: jsonMap(t, conformancePlan)}, nil, "repo app: no base")
	if pc.Undeclared != nil || pc.UnchangedObligations != nil || pc.UnchangedWrites != nil {
		t.Fatalf("an unreadable diff must compute no set difference: %+v", pc)
	}
	if s := renderPlanConformance(pc); !strings.Contains(s, "could not be read") || !strings.Contains(s, "repo app: no base") {
		t.Fatalf("the section must name why the facts are missing:\n%s", s)
	}
	if renderPlanConformance(nil) != "" {
		t.Fatal("no facts, no section")
	}
}

func TestChangedFilesAgainstOriginDefault(t *testing.T) {
	caps := t.TempDir()
	dir := gitRepo(t, caps, "app", map[string]string{"a.go": "a", "old.go": "o"})
	reposDir := filepath.Join(caps, capsule.DirRepos)
	if _, reason := changedFiles(reposDir); !strings.Contains(reason, "origin default branch") {
		t.Fatalf("a repo without a fetched origin default must name the missing base, got %q", reason)
	}
	runGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	writeFile(t, dir, "a.go", "a2")
	writeFile(t, dir, "new.go", "n")
	runGit(t, dir, "rm", "-q", "old.go")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "work")
	writeFile(t, dir, "uncommitted.go", "u")
	got, reason := changedFiles(reposDir)
	if reason != "" {
		t.Fatalf("unexpected unavailable reason %q", reason)
	}
	if want := map[string][]string{"app": {"a.go", "new.go", "old.go"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("changed = %v, want %v (committed diff since the merge base, deletions included)", got, want)
	}
}

func TestPlanConformanceSwitchOnlyOnVerification(t *testing.T) {
	wf, err := parseWorkflow([]byte("steps:\n  - { id: plan, context_policy: fresh }\n  - { id: code, plan_conformance: true }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !wf.Steps[1].PlanConformance {
		t.Fatal("plan_conformance must parse onto the step spec")
	}
	if err = validateWorkflow(wf); err == nil || !strings.Contains(err.Error(), "plan_conformance") {
		t.Fatalf("plan_conformance off the verification entry must be refused, got %v", err)
	}
}

func TestPlanConformanceRidesThePrompt(t *testing.T) {
	pc := foldPlanConformance(planConformanceInputs{plan: jsonMap(t, conformancePlan)}, map[string][]string{"app": {"x.go"}}, "")
	p := assembleStepPrompt(promptParts{StepID: reviewStepID, Definition: "review body", PlanConformance: renderPlanConformance(pc)})
	if !strings.Contains(p, "\n"+planConformanceHeading+"\n") || !strings.Contains(p, "`x.go` — blocking") {
		t.Fatalf("the review prompt must carry the facts section:\n%s", p)
	}
}

// The test files an earlier authoring step declared (`output.test_files`)
// and committed are declared by that step's own output — the plan is told
// not to list them — so a committed authored test file must never be
// reported as changed-but-undeclared (the review rewind's false blocking
// fact on every feature-full run in required mode).
func TestPlanConformanceAuthoredTestFilesCountAsDeclared(t *testing.T) {
	in := planConformanceInputs{plan: jsonMap(t, conformancePlan),
		authored: []string{"test/w78_acceptance_test.go"}}
	pc := foldPlanConformance(in, map[string][]string{"app": {"a.go", "test/w78_acceptance_test.go", "x.go"}}, "")
	if want := []string{"x.go"}; !reflect.DeepEqual(pc.Undeclared, want) {
		t.Fatalf("undeclared = %v, want %v (an authored test file is declared by its authoring step's test_files output)", pc.Undeclared, want)
	}
	// They are not plan writes either: no unchanged-write fact, no
	// obligation mapping line.
	for _, facts := range [][]string{pc.UnchangedWrites, pc.UnchangedObligations} {
		for _, p := range facts {
			if p == "test/w78_acceptance_test.go" {
				t.Fatalf("an authored test file is a plain declared write, never a plan fact: %+v", pc)
			}
		}
	}
	if strings.Contains(renderPlanConformance(pc), "test/w78_acceptance_test.go") {
		t.Fatalf("an authored test file is never named in the section: %+v", pc)
	}
	// Without the authored set the same file is the blocking fact it used
	// to be — the test proves the fold, not an always-empty set.
	pc = foldPlanConformance(planConformanceInputs{plan: jsonMap(t, conformancePlan)}, map[string][]string{"app": {"a.go", "test/w78_acceptance_test.go", "x.go"}}, "")
	if want := []string{"test/w78_acceptance_test.go", "x.go"}; !reflect.DeepEqual(pc.Undeclared, want) {
		t.Fatalf("without the authored set undeclared = %v, want %v", pc.Undeclared, want)
	}
}

// The lock-held snapshot folds the authored set from the earlier done
// steps' test_files outputs (keyed on the output field, not a step
// name), reusing the authored-tests gate's own fold.
func TestPlanConformanceInputsFoldAuthoredTestFiles(t *testing.T) {
	d := &Driver{wi: WIInfo{ID: "wi_1"},
		workflow: &Workflow{Steps: []StepSpec{
			{ID: planStepID}, {ID: "e2e-author"}, {ID: "code"}, {ID: reviewStepID}, {ID: "later-author"}}},
		exec: &capsule.Execution{Steps: map[string]*capsule.Step{
			planStepID: {Status: capsule.StepDone, Output: jsonMap(t, conformancePlan)},
			"e2e-author": {Status: capsule.StepDone, Output: map[string]any{
				"test_files":       []any{"test/w78_acceptance_test.go"},
				testFilesCommitKey: map[string]any{"app": "abc123"}}},
			// A later authoring step (after the judged step) must never leak in.
			"later-author": {Status: capsule.StepDone, Output: map[string]any{
				"test_files":       []any{"test/late_test.go"},
				testFilesCommitKey: map[string]any{"app": "abc123"}}},
		}}}
	in, ok := d.planConformanceInputsLocked(reviewStepID)
	if !ok {
		t.Fatal("a review dispatch on a lane with a done plan step must receive the facts")
	}
	if want := []string{"test/w78_acceptance_test.go"}; !reflect.DeepEqual(in.authored, want) {
		t.Fatalf("inputs.authored = %v, want %v", in.authored, want)
	}
}

// a finding-backed extension stays declared across build
// rounds. A fresh rewind resets the build step's live output, so a round
// that does not re-cite (the agent re-derives from scratch) used to drop
// the round-2 citation and flip the file back to a blocking
// changed-but-undeclared fact; the fold now reads the citation from the
// archived round and marks it carried.
func TestPlanConformanceCitationSurvivesALaterFreshRound(t *testing.T) {
	d, sp, _ := runEnv(t, spec(planStepID), spec("code"), gateSpec(reviewStepID, "code"))
	sp.script = []fakeEntry{
		done(conformancePlan), // s1 plan
		done(`{}`),            // s2 code
		gateFAIL(),            // s3 review FAIL -> code
		done(`{"declared_extensions":[{"path":"x.go","finding_id":"s3#1"}]}`), // s4 code cites
		gateFAIL(),                 // s5 review FAIL -> code (fresh again)
		done(`{}`),                 // s6 code: does not re-cite
		done(`{"verdict":"pass"}`), // s7 review
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := dispatchedSteps(sp); got != "plan,code,review,code,review,code,review" {
		t.Fatalf("dispatch order %s", got)
	}
	want := []declaredExtension{{Path: "x.go", FindingID: "s3#1", Carried: true}}
	if pc := sp.spawnAt(6).PlanConformance; pc == nil || !reflect.DeepEqual(pc.Cited, want) {
		t.Fatalf("the third review's facts must still count the round-2 citation as carried: %+v", pc)
	}
	d.mu.Lock()
	in, ok := d.planConformanceInputsLocked(reviewStepID)
	d.mu.Unlock()
	if !ok {
		t.Fatal("a review on a lane with a done plan receives the facts")
	}
	pc := foldPlanConformance(in, map[string][]string{"app": {"a.go", "docs/a.md", "x.go", "y.go"}}, "")
	if want := []string{"y.go"}; !reflect.DeepEqual(pc.Undeclared, want) {
		t.Fatalf("undeclared = %v, want %v: the cited x.go must not flip back to changed-but-undeclared", pc.Undeclared, want)
	}
	if l := w78Line(renderPlanConformance(pc), "`x.go`"); !strings.Contains(l, "s3#1") || !strings.Contains(l, "earlier build round") {
		t.Fatalf("the carried citation must echo its finding and say it is carried, got %q", l)
	}
}

// The union covers the build rounds since the plan only: a build attempt
// archived before the plan's acceptance belongs to an earlier plan's era
// and never counts; within the era a live citation of the same path wins
// over the archived one, and an archived malformed entry is listed as not
// counted, marked as an earlier round's.
func TestPlanConformanceInputsUnionBuildRoundsSinceThePlan(t *testing.T) {
	stamp := func(s int) string { return time.Date(2026, 10, 28, 12, 0, s, 0, time.UTC).Format(time.RFC3339Nano) }
	d := &Driver{wi: WIInfo{ID: "wi_1"},
		workflow: &Workflow{Steps: []StepSpec{{ID: planStepID}, {ID: "code"}, {ID: reviewStepID, Gate: true, OnFail: &OnFail{Target: "code"}}}},
		exec: &capsule.Execution{
			RewindStats: capsule.RewindStats{Count: 2, Rewinds: []capsule.RewindRecord{
				{Gate: reviewStepID, ToStep: "code", Charged: true}, {Gate: reviewStepID, ToStep: "code", Charged: true}}},
			Steps: map[string]*capsule.Step{
				planStepID: {Status: capsule.StepDone, Output: jsonMap(t, conformancePlan),
					Attempts: []capsule.Attempt{{Cause: "accepted", AttemptSeq: 1, ArchivedAt: stamp(10)}}},
				"code": {Status: capsule.StepDone,
					Output: jsonMap(t, `{"declared_extensions":[{"path":"w.go","finding_id":"s9#2"}]}`),
					Attempts: []capsule.Attempt{
						{Cause: CauseSupersededByRewind, AttemptSeq: 1, ArchivedAt: stamp(5),
							Result: json.RawMessage(`{"declared_extensions":[{"path":"old.go","finding_id":"s2#1"}]}`)},
						{Cause: "accepted", AttemptSeq: 2, ArchivedAt: stamp(20)},
						{Cause: CauseGateFailRewind, AttemptSeq: 3, ArchivedAt: stamp(30), RewindGate: reviewStepID,
							Result: json.RawMessage(`{"declared_extensions":[{"path":"x.go","finding_id":"s3#1"},{"path":"w.go","finding_id":"s3#2"},{"path":"z.go","finding_id":"later"}]}`)},
						{Cause: CauseGateFailRewind, AttemptSeq: 4, ArchivedAt: stamp(40), RewindGate: reviewStepID,
							Result: json.RawMessage(`null`)},
					}},
			}}}
	in, ok := d.planConformanceInputsLocked(reviewStepID)
	if !ok {
		t.Fatal("a review on a lane with a done plan receives the facts")
	}
	pc := foldPlanConformance(in, map[string][]string{"app": {"a.go", "docs/a.md", "old.go", "w.go", "x.go", "z.go"}}, "")
	if want := []string{"old.go", "z.go"}; !reflect.DeepEqual(pc.Undeclared, want) {
		t.Fatalf("undeclared = %v, want %v (a pre-plan round never counts; a malformed id never counts)", pc.Undeclared, want)
	}
	want := []declaredExtension{{Path: "x.go", FindingID: "s3#1", Carried: true}, {Path: "w.go", FindingID: "s9#2"}}
	if !reflect.DeepEqual(pc.Cited, want) {
		t.Fatalf("cited = %+v, want %+v (the live citation of w.go wins over the archived one)", pc.Cited, want)
	}
	if len(pc.Ignored) != 1 || !strings.Contains(pc.Ignored[0], "`z.go`") || !strings.Contains(pc.Ignored[0], "earlier build round") {
		t.Fatalf("ignored = %v, want only z.go's malformed entry, marked as an earlier round's", pc.Ignored)
	}
}
