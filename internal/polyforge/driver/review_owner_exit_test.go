package driver

// review_owner_exit_test.go: an all-off-graph review FAIL parks the
// owner exit instead of rewinding the build step; a mixed FAIL still
// rewinds; waive clears exactly the named findings; amend-upstream lands
// one ruling and routes it (suite gap → the authoring step append-only,
// anything else → the build step), never re-running spec or plan.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// exitLane: spec → plan → e2e-author → code → review (gate → code).
func exitLane(t *testing.T) (*Driver, string) {
	t.Helper()
	d, _, _, execPath := testEnv(t, spec("spec"), spec(planStepID), spec(e2eAuthorStepID), spec("code"), gateSpec(reviewStepID, "code"))
	mustInit(t, d)
	n := int64(1)
	for _, id := range []string{"spec", planStepID, e2eAuthorStepID, "code"} {
		mustStart(t, d, id, n)
		if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: id, SpawnN: n, Result: completedResult(id, n, `{"summary":"`+id+`"}`)}); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		n++
	}
	return d, execPath
}

// reviewFail delivers one review FAIL at ordinal n with the given findings.
func reviewFail(t *testing.T, d *Driver, n int64, findings string) {
	t.Helper()
	mustStart(t, d, reviewStepID, n)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: reviewStepID, SpawnN: n,
		Result: &StepResult{StepID: reviewStepID, SpawnN: n, Outcome: OutcomeGateFailed,
			Output: json.RawMessage(`{"verdict":"fail","fail_reason":"r","findings":` + findings + `}`)}}); err != nil {
		t.Fatalf("review fail: %v", err)
	}
}

func answerExit(t *testing.T, d *Driver, text string) {
	t.Helper()
	if err := d.Answer(ReviewOwnerExitQuestionID, text, "owner"); err != nil {
		t.Fatalf("answer: %v", err)
	}
}

const twoOffGraph = `[{"severity":"blocking","repair_target":"spec","detail":"spec wording"},{"severity":"blocking","repair_target":"declared-obligation","detail":"stale doc"}]`

func TestAllOffGraphFailParksOwnerExit(t *testing.T) {
	d, execPath := exitLane(t)
	reviewFail(t, d, 5, twoOffGraph)
	x := readExec(t, execPath)
	q := x.Steps[reviewStepID].PendingQuestion
	if q == nil || q.ID != ReviewOwnerExitQuestionID {
		t.Fatalf("an all-off-graph FAIL must park the owner exit, got %+v (current %s)", q, x.CurrentStep)
	}
	for _, want := range []string{"amend-upstream", "waive", "cancel", "s5#1", "s5#2"} {
		if !strings.Contains(q.Question, want) {
			t.Errorf("the exit question must name %q: %s", want, q.Question)
		}
	}
	if x.CurrentStep != reviewStepID || x.Steps["code"].Status != capsule.StepDone || len(x.RewindStats.Rewinds) != 0 {
		t.Fatalf("the exit spends no code edge and rewinds nothing: current %s code %s rewinds %+v", x.CurrentStep, x.Steps["code"].Status, x.RewindStats)
	}
}

func TestMixedFailRewindsThenExitFires(t *testing.T) {
	d, execPath := exitLane(t)
	reviewFail(t, d, 5, `[{"severity":"blocking","repair_target":"code","detail":"bug"},{"severity":"blocking","repair_target":"spec","detail":"spec wording"}]`)
	x := readExec(t, execPath)
	if x.Steps[reviewStepID].PendingQuestion != nil || x.CurrentStep != "code" || len(x.RewindStats.Rewinds) != 1 ||
		x.RewindStats.Rewinds[0].Class != capsule.RewindCharged {
		t.Fatalf("a mixed FAIL must rewind code as today: current %s %+v", x.CurrentStep, x.RewindStats)
	}
	if lr := x.Steps[reviewStepID].LastReviewRound; lr == nil || lr.Blocking != 1 {
		t.Fatalf("the off-graph finding rides along uncounted: %+v", lr)
	}
	mustStart(t, d, "code", 6)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "code", SpawnN: 6, Result: completedResult("code", 6, `{}`)}); err != nil {
		t.Fatal(err)
	}
	reviewFail(t, d, 7, `[{"severity":"blocking","repair_target":"spec","detail":"spec wording","residual_of":"s5#2"}]`)
	if q := readExec(t, execPath).Steps[reviewStepID].PendingQuestion; q == nil || q.ID != ReviewOwnerExitQuestionID {
		t.Fatalf("once no code finding remains the exit fires, got %+v", q)
	}
}

func TestWaiveClearsOnlyNamedFindings(t *testing.T) {
	d, execPath := exitLane(t)
	reviewFail(t, d, 5, twoOffGraph)
	answerExit(t, d, "waive everything, it is fine")
	x := readExec(t, execPath)
	if q := x.Steps[reviewStepID].PendingQuestion; q == nil || !strings.Contains(q.Question, "clears nothing") {
		t.Fatalf("a blanket waive must be refused and re-asked, got %+v", q)
	}
	if len(x.Steps[reviewStepID].OwnerRulings) != 0 {
		t.Fatalf("a blanket waive records nothing: %+v", x.Steps[reviewStepID].OwnerRulings)
	}
	answerExit(t, d, "waive s5#1 s9#1 — the spec wording is the owner's choice")
	x = readExec(t, execPath)
	rs := x.Steps[reviewStepID].OwnerRulings
	if len(rs) != 1 || rs[0].Kind != OwnerAnswerWaive || strings.Join(rs[0].Findings, ",") != "s5#1" {
		t.Fatalf("the waive must clear exactly the named finding of this round: %+v", rs)
	}
	if rs[0].Answer != "waive s5#1 s9#1 — the spec wording is the owner's choice" {
		t.Fatalf("the owner's words are kept verbatim: %q", rs[0].Answer)
	}
	if x.Steps[reviewStepID].PendingQuestion != nil || x.CurrentStep != reviewStepID || len(x.RewindStats.Rewinds) != 0 {
		t.Fatalf("a waive re-runs review in place, no rewind: current %s %+v", x.CurrentStep, x.RewindStats)
	}
	if other := readGrillQA(t, d); !strings.Contains(other, "Findings addressed: s5#1.") || strings.Contains(other, "s5#2.") {
		t.Fatalf("the ruling lands naming only the waived finding:\n%s", other)
	}
}

func readGrillQA(t *testing.T, d *Driver) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(d.wi.CapsuleDir, grillQAPath))
	if err != nil {
		t.Fatalf("grill-qa.md: %v", err)
	}
	return string(raw)
}

func TestAmendUpstreamRoutesSpecToCodeAndLandsOnce(t *testing.T) {
	d, execPath := exitLane(t)
	if err := os.MkdirAll(filepath.Join(d.wi.CapsuleDir, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.wi.CapsuleDir, grillQAPath), []byte("# grill\n\nRound 1.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := readExec(t, execPath)
	reviewFail(t, d, 5, twoOffGraph)
	const words = "amend-upstream: the spec's THEN clause is corrected to the shipped behaviour"
	answerExit(t, d, words)
	x := readExec(t, execPath)
	if x.CurrentStep != "code" || x.Steps["code"].PendingRewindInput == nil || !strings.Contains(x.Steps["code"].PendingRewindInput.Reason, words) {
		t.Fatalf("a spec/obligation amendment routes to the build step with the ruling: current %s %+v", x.CurrentStep, x.Steps["code"].PendingRewindInput)
	}
	if r := x.RewindStats.Rewinds; len(r) != 1 || r[0] != (capsule.RewindRecord{Gate: reviewStepID, ToStep: "code", Class: capsule.RewindOwner}) {
		t.Fatalf("the routed rewind is owner-class: %+v", r)
	}
	for _, id := range []string{"spec", planStepID, e2eAuthorStepID} {
		if x.Steps[id].Status != capsule.StepDone || len(x.Steps[id].Attempts) != len(before.Steps[id].Attempts) {
			t.Fatalf("%s must not be re-run by an amendment: %+v", id, x.Steps[id])
		}
	}
	doc := readGrillQA(t, d)
	if !strings.HasPrefix(doc, "# grill\n\nRound 1.\n") || strings.Count(doc, "## Owner rulings") != 1 || strings.Count(doc, words) != 1 {
		t.Fatalf("one ruling block appended after the existing content:\n%s", doc)
	}
	d.landOwnerRulings() // a restart re-check never duplicates
	if again := readGrillQA(t, d); again != doc {
		t.Fatalf("landing is idempotent:\n%s", again)
	}
}

func TestAmendUpstreamSuiteGapRoutesToAuthoringStep(t *testing.T) {
	d, execPath := exitLane(t)
	reviewFail(t, d, 5, `[{"severity":"blocking","repair_target":"authored-suite","detail":"no scenario for the cancel path"}]`)
	answerExit(t, d, "amend-upstream: add the cancel scenario")
	x := readExec(t, execPath)
	pri := x.Steps[e2eAuthorStepID].PendingRewindInput
	if x.CurrentStep != e2eAuthorStepID || pri == nil || pri.Note != OwnerAmendSuiteNote {
		t.Fatalf("a suite gap re-runs the authoring step append-only: current %s %+v", x.CurrentStep, pri)
	}
	if x.Steps["spec"].Status != capsule.StepDone || x.Steps[planStepID].Status != capsule.StepDone {
		t.Fatal("spec and plan are never re-run")
	}
}

func TestOtherAnswerReasksOwnerExit(t *testing.T) {
	d, execPath := exitLane(t)
	reviewFail(t, d, 5, twoOffGraph)
	answerExit(t, d, "please just fix it")
	q := readExec(t, execPath).Steps[reviewStepID].PendingQuestion
	if q == nil || q.ID != ReviewOwnerExitQuestionID || !strings.Contains(q.Question, "not one of amend-upstream, waive or cancel") {
		t.Fatalf("an unrecognised answer is asked again, never mapped: %+v", q)
	}
}
