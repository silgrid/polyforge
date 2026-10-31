package driver

// e2e_author_allgreen_test.go: an all-green authored baseline parks the
// owner question instead of advancing to code; rewind-to-spec is an
// owner-routed rewind of [spec, e2e-author]; skip re-runs e2e-author fresh
// and its empty completion is accepted.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

const greenBaseline = `{"scenario_titles":["t"],"baseline":[{"title":"t","status":"pass","evidence":"already green"}]}`

func allGreenLane(t *testing.T) (*Driver, string) {
	t.Helper()
	d, _, _, execPath := testEnv(t, spec(specStepID), spec(planStepID), spec(e2eAuthorStepID), spec("code"))
	mustInit(t, d)
	for i, id := range []string{specStepID, planStepID} {
		n := int64(i + 1)
		mustStart(t, d, id, n)
		resultAt(t, d, id, n, OutcomeCompleted, `{}`, nil)
	}
	mustStart(t, d, e2eAuthorStepID, 3)
	resultAt(t, d, e2eAuthorStepID, 3, OutcomeCompleted, greenBaseline, nil)
	return d, execPath
}

func TestAllGreenBaselineParksOwnerQuestion(t *testing.T) {
	d, execPath := allGreenLane(t)
	x := readExec(t, execPath)
	q := x.Steps[e2eAuthorStepID].PendingQuestion
	if q == nil || q.ID != E2EAllGreenQuestionID || x.Steps[e2eAuthorStepID].Status != capsule.StepInProgress {
		t.Fatalf("an all-green baseline must park the owner question, not complete: %+v", x.Steps[e2eAuthorStepID])
	}
	for _, want := range []string{"rewind-to-spec", "skip", "cancel"} {
		if !strings.Contains(q.Question, want) {
			t.Errorf("the question must offer %q", want)
		}
	}
	// a mixed or red baseline is an ordinary completion
	if allGreenBaseline(jsonMap(t, `{"baseline":[{"status":"pass"},{"status":"fail"}]}`)) || allGreenBaseline(jsonMap(t, `{"baseline":[]}`)) {
		t.Fatal("only an all-pass, non-empty baseline is all-green")
	}
	_ = d
}

func TestAllGreenRewindToSpecIsOwnerRouted(t *testing.T) {
	d, execPath := allGreenLane(t)
	if err := d.Answer(E2EAllGreenQuestionID, "rewind-to-spec: the THEN clause must name the new flag", "owner"); err != nil {
		t.Fatal(err)
	}
	x := readExec(t, execPath)
	if x.CurrentStep != specStepID || x.Steps[e2eAuthorStepID].Status != capsule.StepPending || x.Steps[specStepID].PendingRewindInput == nil {
		t.Fatalf("rewind-to-spec rewinds [spec, e2e-author]: current %s %+v", x.CurrentStep, x.Steps[specStepID].PendingRewindInput)
	}
	if r := x.RewindStats.Rewinds; len(r) != 1 || r[0] != (capsule.RewindRecord{Gate: e2eAuthorStepID, ToStep: specStepID, Class: capsule.RewindOwner}) || countTowardTotal(r) != 0 {
		t.Fatalf("the rewind is owner-routed (no edge slot, no max_total): %+v", r)
	}
}

func TestAllGreenSkipCompletesEmpty(t *testing.T) {
	d, execPath := allGreenLane(t)
	if err := d.Answer(E2EAllGreenQuestionID, "skip: the flag already shipped in the last release", "owner"); err != nil {
		t.Fatal(err)
	}
	x := readExec(t, execPath)
	if st := x.Steps[e2eAuthorStepID]; st.PendingQuestion != nil || st.Status != capsule.StepInProgress || st.Supervision.NextDispatch != capsule.NextDispatchFresh {
		t.Fatalf("skip re-dispatches e2e-author fresh with the answer: %+v", st)
	}
	mustStart(t, d, e2eAuthorStepID, 4)
	resultAt(t, d, e2eAuthorStepID, 4, OutcomeCompleted, `{"e2e_author_mode":"skipped-with-rationale","skip_rationale":"the flag already shipped","scenario_titles":[],"baseline":[]}`, nil)
	if st := readExec(t, execPath).Steps[e2eAuthorStepID]; st.Status != capsule.StepDone {
		t.Fatalf("the recorded skip completes: %+v", st)
	}
}
