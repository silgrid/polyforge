package driver

// rewind_owner_routed_test.go: a rewind an owner's explicit answer
// routed (a deliver FAIL a human answered in its attempt chain — "back to
// code", 「全量重验」) charges neither its edge slot nor max_total, while
// the automatic rewinds keep the runaway guard and the per-edge charge
// exactly as before.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// driveAnsweredDeliverFail runs build, then a deliver that asks, gets
// a human answer and FAILs: the owner's "back to code". Returns the next
// spawn ordinal.
func driveAnsweredDeliverFail(t *testing.T, d *Driver, n int64) int64 {
	t.Helper()
	mustStart(t, d, "build", n)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: n, Result: completedResult("build", n, `{}`)}); err != nil {
		t.Fatalf("build: %v", err)
	}
	n++
	mustStart(t, d, deliverStepID, n)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: deliverStepID, SpawnN: n,
		Result: &StepResult{StepID: deliverStepID, SpawnN: n, Outcome: OutcomeNeedsInput, QuestionID: "merge", Question: "merge or back to code?"}}); err != nil {
		t.Fatalf("deliver question: %v", err)
	}
	if err := d.transition(TransitionRequest{Kind: TransAnswer, QuestionID: "merge", Reason: "back to code", AnsweredBy: "owner", SpawnN: n}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	n++
	mustStart(t, d, deliverStepID, n)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: deliverStepID, SpawnN: n,
		Result: &StepResult{StepID: deliverStepID, SpawnN: n, Outcome: OutcomeGateFailed}}); err != nil {
		t.Fatalf("deliver fail: %v", err)
	}
	return n + 1
}

func TestOwnerRoutedRewindAtGlobalCapContinues(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("check", "build"), gateSpec(deliverStepID, "build"))
	d.workflow.MaxTotal = 1
	d.workflow.MaxPerStep = 1
	mustInit(t, d)
	driveEdge(t, d, "build", "check", 1) // the one automatic rewind: max_total is now spent
	n := int64(3)
	for round := 0; round < 3; round++ { // past both bounds: the owner's route is never refused
		n = driveAnsweredDeliverFail(t, d, n)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusRunning {
		t.Fatalf("owner-routed rewinds at the global cap must continue, got %q %+v", exec.WorkflowStatus, exec.RewindStats)
	}
	if exec.RewindStats.Count != 4 || len(exec.RewindStats.Rewinds) != 4 {
		t.Fatalf("the ledger keeps count == len(rewinds): %+v", exec.RewindStats)
	}
	for i, r := range exec.RewindStats.Rewinds[1:] {
		if r != (capsule.RewindRecord{Gate: deliverStepID, ToStep: "build", Class: capsule.RewindOwner}) {
			t.Fatalf("owner rewind %d = %+v, want the owner class", i+1, r)
		}
	}
	if got := countTowardTotal(exec.RewindStats.Rewinds); got != 1 {
		t.Fatalf("max_total counts %d records, want 1 (owner-routed rewinds never increment it)", got)
	}
	// the automatic edge still exhausts at its unchanged number
	driveEdge(t, d, "build", "check", n)
	if exec = readExec(t, execPath); exec.WorkflowStatus != StatusFailedRewindExhausted {
		t.Fatalf("an automatic rewind at the cap must still exhaust, got %q", exec.WorkflowStatus)
	}
}

func TestUnansweredDeliverFailStaysCharged(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec(deliverStepID, "build"))
	d.workflow.MaxTotal = 4
	d.workflow.MaxPerStep = 1
	mustInit(t, d)
	driveEdge(t, d, "build", deliverStepID, 1)
	if exec := readExec(t, execPath); exec.RewindStats.Rewinds[0].Class != capsule.RewindCharged {
		t.Fatalf("a deliver FAIL no human answered is not owner-routed: %+v", exec.RewindStats.Rewinds)
	}
	driveEdge(t, d, "build", deliverStepID, 3)
	if exec := readExec(t, execPath); exec.WorkflowStatus != StatusFailedRewindExhausted {
		t.Fatalf("the unanswered deliver edge still spends its slot: %q", exec.WorkflowStatus)
	}
}

func TestUnchargedRewindsStillHitGlobalCap(t *testing.T) {
	recs := []capsule.RewindRecord{
		{Gate: reviewStepID, ToStep: "code", Class: capsule.RewindUncharged},
		{Gate: verificationStepID, ToStep: "code", Class: capsule.RewindUncharged},
		{Gate: deliverStepID, ToStep: "code", Class: capsule.RewindOwner},
		{Gate: fixStepID, ToStep: rootCauseStepID, Class: capsule.RewindCharged},
	}
	if got := countTowardTotal(recs); got != 3 {
		t.Fatalf("max_total counts charged and uncharged records (%d), want 3", got)
	}
}
