package driver

// gate_failure_count_repro_test.go: B2 — a gate's failure
// counter must not accumulate across its own FAIL rounds. The gate
// delivers a verdict, its T and AnswerBase are reset and the re-entry
// dispatches fresh, but Supervision.FailureCount/LongTaskTimeouts are
// carried over, so deaths that belonged to earlier rounds push a later
// round to the max-per-step bound early.
//
// Written RED against the current driver: two counted deaths before the
// FAIL verdict leave FailureCount=2 on the gate's re-entry; the intended
// re-entry window starts at 0 (the same reset a rewind gives its
// target).

import (
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestGateFailRewindDoesNotCarryPriorRoundsFailureCount(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("fix"), gateSpec("review", "fix"))
	mustInit(t, d)

	mustStart(t, d, "fix", 1)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "fix", SpawnN: 1,
		Result: completedResult("fix", 1, `{}`),
	}); err != nil {
		t.Fatalf("fix result: %v", err)
	}

	// Round 1 of the gate: two counted same-model deaths before the
	// verdict arrives (each re-dispatch resumes the step in place).
	mustStart(t, d, "review", 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepFailed, StepID: "review", SpawnN: 2,
		Failure: &StepFailure{Cause: FailExitOther, Detail: "died in round 1 (a)"},
	}); err != nil {
		t.Fatalf("review death 1: %v", err)
	}
	mustStart(t, d, "review", 3)
	if err := d.transition(TransitionRequest{
		Kind: TransStepFailed, StepID: "review", SpawnN: 3,
		Failure: &StepFailure{Cause: FailExitOther, Detail: "died in round 1 (b)"},
	}); err != nil {
		t.Fatalf("review death 2: %v", err)
	}
	if got := readExec(t, execPath).Steps["review"].Supervision.FailureCount; got != 2 {
		t.Fatalf("setup: the two round-1 deaths must count 2, got %d", got)
	}

	// The verdict lands: the gate stays in_progress and rewinds to fix.
	mustStart(t, d, "review", 4)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 4,
		Result: &StepResult{StepID: "review", SpawnN: 4, Outcome: OutcomeGateFailed},
	}); err != nil {
		t.Fatalf("gate fail: %v", err)
	}

	x := readExec(t, execPath)
	if x.Steps["review"].Status != capsule.StepInProgress {
		t.Fatalf("the gate stays in_progress across its own rewind: %+v", x.Steps["review"])
	}
	if sup := x.Steps["review"].Supervision; sup == nil || sup.FailureCount != 0 {
		t.Fatalf("the gate's re-entry must start a fresh failure window, got %+v", sup)
	}
}
