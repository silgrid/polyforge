package driver

// bugfix_dead_end_test.go: the bug-fix dead ends ask the owner — a repro
// reporting reproduces:false, and a second inconclusive root cause — and
// a repro re-run after a fix → root-cause rewind is pointed at the
// recorded pre-fix base.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func bugfixLane(t *testing.T) (*Driver, string) {
	t.Helper()
	d, _, _, execPath := testEnv(t, spec(reproStepID), gateSpec(rootCauseStepID, reproStepID), spec(fixStepID))
	mustInit(t, d)
	return d, execPath
}

func resultAt(t *testing.T, d *Driver, step string, n int64, outcome Outcome, out string, heads map[string]string) {
	t.Helper()
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: step, SpawnN: n, ReproHeads: heads,
		Result: &StepResult{StepID: step, SpawnN: n, Outcome: outcome, Output: json.RawMessage(out)}}); err != nil {
		t.Fatalf("%s result: %v", step, err)
	}
}

func TestNotReproducedParksOwnerQuestion(t *testing.T) {
	d, execPath := bugfixLane(t)
	mustStart(t, d, reproStepID, 1)
	resultAt(t, d, reproStepID, 1, OutcomeCompleted, `{"reproduces":false}`, nil)
	x := readExec(t, execPath)
	q := x.Steps[reproStepID].PendingQuestion
	if q == nil || q.ID != ReproNotReproducedQuestionID || x.Steps[reproStepID].Status != capsule.StepInProgress || len(x.RewindStats.Rewinds) != 0 {
		t.Fatalf("reproduces:false must park the owner question, spend no rewind: %+v %+v", q, x.RewindStats)
	}
	for _, want := range []string{"`re-run-repro: <evidence>`", "`continue: <guidance>`", "`cancel`"} {
		if !strings.Contains(q.Question, want) {
			t.Errorf("the question must offer %s: %s", want, q.Question)
		}
	}
	if err := d.Answer(ReproNotReproducedQuestionID, "maybe later", "owner"); err != nil {
		t.Fatal(err)
	}
	if q := readExec(t, execPath).Steps[reproStepID].PendingQuestion; q == nil || !strings.Contains(q.Question, "not one of") {
		t.Fatalf("an unrecognised answer is asked again: %+v", q)
	}
	if err := d.Answer(ReproNotReproducedQuestionID, "re-run-repro: it only fails with TZ=UTC", "owner"); err != nil {
		t.Fatal(err)
	}
	x = readExec(t, execPath)
	if st := x.Steps[reproStepID]; st.PendingQuestion != nil || st.Status != capsule.StepInProgress || st.PendingRewindInput == nil ||
		!strings.Contains(st.PendingRewindInput.Reason, "TZ=UTC") || st.Supervision.NextDispatch != capsule.NextDispatchFresh {
		t.Fatalf("re-run-repro re-dispatches repro fresh with the evidence: %+v", st)
	}
	mustStart(t, d, reproStepID, 2)
	resultAt(t, d, reproStepID, 2, OutcomeCompleted, `{"reproduces":false}`, nil)
	if err := d.Answer(ReproNotReproducedQuestionID, "continue: diagnose from the stack trace in the report", "owner"); err != nil {
		t.Fatal(err)
	}
	x = readExec(t, execPath)
	if x.Steps[reproStepID].Status != capsule.StepDone || x.WorkflowStatus != StatusRunning {
		t.Fatalf("continue accepts the report and moves on: %+v", x.Steps[reproStepID])
	}
}

// Review s8#2: the owner's continue guidance at the repro dead end reaches
// the next root-cause dispatch verbatim (root-cause reads only repro's
// artifacts, written before the answer), and the round's acceptance
// clears it.
func TestNotReproducedContinueGuidanceReachesRootCause(t *testing.T) {
	d, execPath := bugfixLane(t)
	mustStart(t, d, reproStepID, 1)
	resultAt(t, d, reproStepID, 1, OutcomeCompleted, `{"reproduces":false}`, nil)
	const answer = "continue: diagnose from the stack trace in the report"
	if err := d.Answer(ReproNotReproducedQuestionID, answer, "owner"); err != nil {
		t.Fatal(err)
	}
	sp, _, err := d.prepareDispatch(rootCauseStepID)
	if err != nil {
		t.Fatal(err)
	}
	if sp.RewindInput == nil || sp.RewindInput.ToStep != rootCauseStepID || !strings.HasSuffix(sp.RewindInput.Reason, "\n\n"+answer) {
		t.Fatalf("the root-cause dispatch must carry the owner's guidance verbatim: %+v", sp.RewindInput)
	}
	mustStart(t, d, rootCauseStepID, 2)
	resultAt(t, d, rootCauseStepID, 2, OutcomeCompleted, `{"verdict":"pass","root_cause":"x","evidence":[{"kind":"code","ref":"a.go","detail":"d"}]}`, nil)
	if pri := readExec(t, execPath).Steps[rootCauseStepID].PendingRewindInput; pri != nil {
		t.Fatalf("the guidance is consumed by the round that carried it: %+v", pri)
	}
}

func TestSecondInconclusiveRootCauseAsksOwner(t *testing.T) {
	d, execPath := bugfixLane(t)
	n := int64(1)
	for round := 0; round < 2; round++ {
		mustStart(t, d, reproStepID, n)
		resultAt(t, d, reproStepID, n, OutcomeCompleted, `{"reproduces":true}`, nil)
		n++
		mustStart(t, d, rootCauseStepID, n)
		resultAt(t, d, rootCauseStepID, n, OutcomeGateFailed, `{"verdict":"fail","fail_reason":"inconclusive"}`, nil)
		n++
	}
	x := readExec(t, execPath)
	q := x.Steps[rootCauseStepID].PendingQuestion
	if q == nil || q.ID != RootCauseInconclusiveQuestionID || len(x.RewindStats.Rewinds) != 1 || x.WorkflowStatus != StatusRunning {
		t.Fatalf("the second inconclusive root cause asks instead of a second charged rewind: %+v %+v", q, x.RewindStats)
	}
	if err := d.Answer(RootCauseInconclusiveQuestionID, "re-run-repro: capture the scheduler log", "owner"); err != nil {
		t.Fatal(err)
	}
	x = readExec(t, execPath)
	if x.CurrentStep != reproStepID || len(x.RewindStats.Rewinds) != 2 || x.RewindStats.Rewinds[1].Class != capsule.RewindOwner {
		t.Fatalf("re-run-repro is an owner-routed rewind to repro: %s %+v", x.CurrentStep, x.RewindStats)
	}
}

func TestReproAfterFixRewindUsesPreFixBase(t *testing.T) {
	d, execPath := bugfixLane(t)
	mustStart(t, d, reproStepID, 1)
	resultAt(t, d, reproStepID, 1, OutcomeCompleted, `{"reproduces":true}`, map[string]string{"app": "aaa111"})
	mustStart(t, d, rootCauseStepID, 2)
	resultAt(t, d, rootCauseStepID, 2, OutcomeCompleted, `{"verdict":"pass"}`, nil)
	mustStart(t, d, fixStepID, 3)
	resultAt(t, d, fixStepID, 3, OutcomeCompleted, `{"root_cause_addressed":false,"summary":"only mitigated","commits":[]}`, nil)
	mustStart(t, d, rootCauseStepID, 4)
	resultAt(t, d, rootCauseStepID, 4, OutcomeGateFailed, `{"verdict":"fail","fail_reason":"inconclusive"}`, nil)
	x := readExec(t, execPath)
	pri := x.Steps[reproStepID].PendingRewindInput
	if x.CurrentStep != reproStepID || pri == nil || !strings.Contains(pri.Note, "app@aaa111") || !strings.Contains(pri.Note, "pre-fix base") {
		t.Fatalf("repro after a fix → root-cause rewind must run on the pre-fix base: %s %+v", x.CurrentStep, pri)
	}
	if x.PreFixHeads["app"] != "aaa111" {
		t.Fatalf("the pre-fix base is the first repro's heads: %+v", x.PreFixHeads)
	}
}
