package driver

// cancel_intent_test.go: an owner's cancel the step's agent delivered as
// an `intent: cancel` (intent.go) — the durable cancel intent with the
// blocked park — is dropped by the owner's resume. (The reserved `cancel`
// answer it used to share the path with left with the v1 question,
// workflow contract v2 change 19.)

import (
	"encoding/json"
	"testing"
)

// Review s8#1: the owner's resume supersedes a cancel intent whose cancel
// never landed at the hub — the resumed workflow carries no stale intent
// its next blocked close would act on.
func TestOwnerResumeDropsCancelIntent(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("check", "build"), spec("after"))
	mustInit(t, d)
	n := secondRound(t, d, "check", 1)
	replyAt := ownerReplied(t, d, "check", n, "drop it")
	if err := settleResult(t, d, intentResult("check", n, IntentCancel, replyAt, "", "the owner drops the work item")); err != nil {
		t.Fatal(err)
	}
	if x := readExec(t, execPath); x.CancelRequested == nil || x.WorkflowStatus != StatusBlocked {
		t.Fatalf("precondition: the cancel intent parks blocked with the intent: %+v", x.CancelRequested)
	}
	if err := d.ResumeCurrent(); err != nil {
		t.Fatal(err)
	}
	if x := readExec(t, execPath); x.WorkflowStatus != StatusRunning || x.CancelRequested != nil {
		t.Fatalf("the owner's resume drops the cancel intent: %q %+v", x.WorkflowStatus, x.CancelRequested)
	}
}

// resultAt lands one step result through the transition.
func resultAt(t *testing.T, d *Driver, step string, n int64, outcome Outcome, out string) {
	t.Helper()
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: step, SpawnN: n,
		Result: envelopeOf(&StepResult{StepID: step, SpawnN: n, Outcome: outcome, Output: json.RawMessage(out)})}); err != nil {
		t.Fatalf("%s result: %v", step, err)
	}
}

// envelopeOf shapes a test envelope as the protocol requires
// (J22): an incomplete's blocker written into the
// fixture's output moves to the envelope's top level.
func envelopeOf(res *StepResult) *StepResult {
	var top struct {
		Blocker map[string]any `json:"blocker"`
	}
	if json.Unmarshal(res.Output, &top) != nil {
		return res
	}
	if res.Outcome == OutcomeIncomplete && res.Blocker == nil {
		res.Blocker = top.Blocker
	}
	return res
}
