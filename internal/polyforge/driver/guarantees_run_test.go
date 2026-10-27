package driver

// guarantees_run_test.go: Run-loop tests — the guarantees land on the
// in-process REJECTED-rewrite lane, never the recovery lane.

import (
	"context"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestRunRejectsQuestionlessNeedsInputInProcess(t *testing.T) {
	d, sp, execPath := runEnv(t, StepSpec{ID: "grill"})
	bad := fakeEntry{res: &StepResult{Outcome: OutcomeNeedsInput, Question: "lean or compatible?"}}
	sp.script = []fakeEntry{bad, asks("q_1", "lean or compatible?"), done(`{}`)}
	doneCh := make(chan error, 1)
	go func() { doneCh <- d.Run(context.Background()) }()
	waitUntil(t, "the rewritten question on disk", func() bool {
		exec := tryReadExec(execPath)
		return exec != nil && exec.Steps["grill"].PendingQuestion != nil
	})
	second := sp.spawnAt(1)
	for _, want := range []string{"question_id", "needs_input (question) report", "lean or compatible?"} {
		if !strings.Contains(second.PriorRejection, want) {
			t.Fatalf("feedback must contain %q, got %q", want, second.PriorRejection)
		}
	}
	if err := d.Answer("q_1", "lean", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := <-doneCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunHumanGateRejectsCompletedUntilAnswered(t *testing.T) {
	d, sp, execPath := runEnv(t, StepSpec{ID: "grill", RequiresHumanAnswer: true})
	sp.script = []fakeEntry{done(`{}`), asks("q_1", "ok?"), done(`{}`)}
	doneCh := make(chan error, 1)
	go func() { doneCh <- d.Run(context.Background()) }()
	waitUntil(t, "the question after the rejected completion", func() bool {
		exec := tryReadExec(execPath)
		return exec != nil && exec.Steps["grill"].PendingQuestion != nil
	})
	if fb := sp.spawnAt(1).PriorRejection; !strings.Contains(fb, "requires a human answer: return needs_input first") {
		t.Fatalf("feedback: %q", fb)
	}
	if err := d.Answer("q_1", "yes", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := <-doneCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exec := readExec(t, execPath); exec.Steps["grill"].Status != capsule.StepDone {
		t.Fatalf("step must complete after the human answer: %v", exec.Steps["grill"].Status)
	}
}

func TestRunVerificationRequiresEvidence(t *testing.T) {
	d, sp, execPath := runEnv(t, StepSpec{ID: "verification", RequiresCommandEvidence: true})
	withEv := done(`{}`)
	withEv.res.CommandEvidence = rawEvidence(goodEvidence())
	sp.script = []fakeEntry{done(`{}`), withEv}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(sp.spawns); n != 2 {
		t.Fatalf("evidence-less completion must be re-dispatched once, got %d spawns", n)
	}
	if !strings.Contains(sp.spawnAt(1).PriorRejection, "verification evidence") {
		t.Fatalf("feedback: %q", sp.spawnAt(1).PriorRejection)
	}
	st := readExec(t, execPath).Steps["verification"]
	if st.Status != capsule.StepDone || st.Output["commands"] == nil {
		t.Fatalf("accepted evidence must stay in the stored output: %+v", st)
	}
}

// TestRunHumanGateGateFailRewindNeedsFreshAnswer drives the real
// transitions: a human-gate gate asks, is answered, fails and rewinds;
// on re-entry a completed result without a NEW answer is rejected.
func TestRunHumanGateGateFailRewindNeedsFreshAnswer(t *testing.T) {
	gate := gateSpec("grill-review", "spec")
	gate.RequiresHumanAnswer = true
	d, sp, execPath := runEnv(t, spec("spec"), gate)
	sp.script = []fakeEntry{
		done(`{}`),                 // spec
		asks("q_1", "right?"),      // grill-review round 1
		gateFAIL(),                 // after the human said "not right": rewind to spec
		done(`{}`),                 // spec again
		done(`{"verdict":"PASS"}`), // grill-review without a new answer: rejected
		asks("q_2", "now right?"),  // the rewrite asks
		done(`{"verdict":"PASS"}`), // after the new answer: accepted
	}
	doneCh := make(chan error, 1)
	go func() { doneCh <- d.Run(context.Background()) }()
	waitUntil(t, "round-1 question", func() bool {
		exec := tryReadExec(execPath)
		return exec != nil && exec.Steps["grill-review"].PendingQuestion != nil
	})
	if err := d.Answer("q_1", "not right", "alice"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "round-2 question after the rejected pass", func() bool {
		exec := tryReadExec(execPath)
		if exec == nil {
			return false
		}
		pq := exec.Steps["grill-review"].PendingQuestion
		return pq != nil && pq.ID == "q_2"
	})
	if fb := sp.spawnAt(5).PriorRejection; !strings.Contains(fb, "requires a human answer: return needs_input first") {
		t.Fatalf("a pass without a fresh answer must be rejected, feedback: %q", fb)
	}
	if err := d.Answer("q_2", "yes", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := <-doneCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted || exec.Steps["grill-review"].Status != capsule.StepDone {
		t.Fatalf("workflow must complete after the fresh answer: %+v", exec.Steps["grill-review"])
	}
}

// polyforge#164 H11: an if_asked human checkpoint (autopilot grill) may
// complete with zero questions — an unattended lane runs on — but the
// skipped checkpoint is recorded as the durable human_gate_unasked event
// (which the watcher turns into an owner notification).
func TestRunIfAskedZeroQuestionPassIsRecorded(t *testing.T) {
	d, sp, execPath := runEnv(t, StepSpec{ID: "grill", HumanAnswerIfAsked: true}, spec("fix"))
	sp.script = []fakeEntry{done(`{}`), done(`{}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exec := readExec(t, execPath); exec.WorkflowStatus != StatusCompleted || len(sp.dispatched()) != 2 {
		t.Fatalf("a zero-question pass must be accepted, not refused: %s %v", exec.WorkflowStatus, sp.dispatched())
	}
	var unasked []loggedEvent
	for _, e := range d.log.(*eventLog).entries {
		if e.kind == EvHumanGateUnasked {
			unasked = append(unasked, e)
		}
	}
	if len(unasked) != 1 || unasked[0].body["step"] != "grill" {
		t.Fatalf("exactly one human_gate_unasked record for grill, got %+v", unasked)
	}
}

// An if_asked checkpoint that asked and got a human answer is a normal
// pass: no unasked record.
func TestRunIfAskedAnsweredPassIsNotFlagged(t *testing.T) {
	d, sp, execPath := runEnv(t, StepSpec{ID: "grill", HumanAnswerIfAsked: true})
	sp.script = []fakeEntry{asks("q_1", "which scope?"), done(`{}`)}
	doneCh := make(chan error, 1)
	go func() { doneCh <- d.Run(context.Background()) }()
	waitUntil(t, "the grill question", func() bool {
		exec := tryReadExec(execPath)
		return exec != nil && exec.Steps["grill"].PendingQuestion != nil
	})
	if err := d.Answer("q_1", "the narrow one", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := <-doneCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, e := range d.log.(*eventLog).entries {
		if e.kind == EvHumanGateUnasked {
			t.Fatalf("an answered checkpoint must not be flagged unasked: %+v", e)
		}
	}
}
