package driver

// deliver_resume_consumed_answer_repro_test.go: the repro of
// (the reported incident: pf v2.20.9, 2026-10-28 UTC).
//
// A human answers the deliver step's `deliver-merge` question "back to
// code: ...". The gate applies it (gate_failed), the driver rewinds to the
// build step, the fix commits, verification passes — and deliver is
// dispatched again. That re-dispatch is handed the step's FULL answer
// archive, including the answer the rewind already consumed, so an agent
// that acts on the "Answered questions (this step's history — continue
// from it)" section replays the old verdict: gate_failed with the very
// same "back to code" text, a SECOND rewind to a build step that has
// nothing left to do, and a work item parked blocked.
//
// The test's spawner stands in for that agent: a deliver dispatch whose
// Answers carry the consumed answer returns gate_failed with it; a
// dispatch without an answer asks the deliver-merge question afresh.
//
// Expected per the WI: a human answer is consumed exactly once. The
// re-dispatched deliver must not replay it — it either applies the merge
// rule or asks a new deliver-merge question, and the rewind count stays 1.
//
// Written RED against the current driver: the post-rewind deliver spawn
// carries the consumed answer, so the stand-in replays it and the workflow
// rewinds a second time.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

const (
	// the incident's answer verbatim (the consumed deliver-merge verdict).
	consumedDeliveryAnswer = "back to code: convert AGENTS.md and docs/agents/test-and-unguarded-assets.md to LF line endings"
	// a second deliver-merge question after the re-entry (the healthy
	// alternative to replaying the consumed answer).
	reentryQuestionID = "deliver-merge-round-2"
)

// consumedAnswerSpawner is the scripted agent of the incident: it applies
// whatever answer archive its spawn hands it, and asks the merge question
// when it was handed none.
type consumedAnswerSpawner struct {
	mu     sync.Mutex
	spawns []StepSpawn
}

func (s *consumedAnswerSpawner) SpawnStep(ctx context.Context, sp StepSpawn) (StepRun, error) {
	s.mu.Lock()
	s.spawns = append(s.spawns, sp)
	s.mu.Unlock()
	var res *StepResult
	switch sp.StepID {
	case deliverStepID:
		if hasConsumedAnswer(sp.Answers) {
			// the reported replay: the agent re-applies the answer it reads
			res = &StepResult{
				Outcome: OutcomeGateFailed,
				Output:  json.RawMessage(`{"verdict":"fail","fail_reason":"` + consumedDeliveryAnswer + `"}`),
			}
		} else {
			res = &StepResult{
				Outcome:    OutcomeNeedsInput,
				QuestionID: reentryQuestionID,
				Question:   "round 2: merge the delivery PR?",
			}
		}
	default:
		res = &StepResult{Outcome: OutcomeCompleted, Output: json.RawMessage(`{}`)}
	}
	return &fakeRun{sp: sp, e: fakeEntry{res: res}}, nil
}

func hasConsumedAnswer(answers []capsule.ArchivedAnswer) bool {
	for _, a := range answers {
		if a.Answer == consumedDeliveryAnswer {
			return true
		}
	}
	return false
}

func (s *consumedAnswerSpawner) at(i int) StepSpawn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spawns[i]
}

// deliverReentryAt answers the index of the THIRD deliver dispatch — the
// one after the human's answer rewound the workflow through fix and
// verification — or -1 while it has not happened. The second deliver
// dispatch is the answer continuation that legitimately applies the human
// verdict and causes the rewind.
func (s *consumedAnswerSpawner) deliverReentryAt() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := 0
	for i, sp := range s.spawns {
		if sp.StepID != deliverStepID {
			continue
		}
		seen++
		if seen == 3 {
			return i
		}
	}
	return -1
}

func TestDeliverReentryDoesNotReplayConsumedAnswer(t *testing.T) {
	dir := t.TempDir()
	execPath := dir + "/" + capsule.ExecutionJSON
	sp := &consumedAnswerSpawner{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{
			Steps:      []StepSpec{spec("fix"), spec("verification"), gateSpec(deliverStepID, "fix")},
			MaxTotal:   4,
			MaxPerStep: 2,
		},
		Spawner: sp,
		Log:     &eventLog{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	doneCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { doneCh <- d.Run(ctx) }()
	// stopRun ends the loop before any assertion fails, so no writer is
	// still touching the temp capsule when the test tears it down.
	stopRun := func() {
		cancel()
		select {
		case <-doneCh:
		case <-time.After(5 * time.Second):
			t.Fatal("the driver loop did not stop")
		}
	}

	// Round 1: deliver asks the deliver-merge question; the human answers
	// "back to code"; the gate applies it and the driver rewinds to fix.
	waitUntil(t, "the deliver-merge question", func() bool {
		exec := tryReadExec(execPath)
		return exec != nil && exec.Steps[deliverStepID] != nil && exec.Steps[deliverStepID].PendingQuestion != nil
	})
	q := readExec(t, execPath).Steps[deliverStepID].PendingQuestion
	if err := d.Answer(q.ID, consumedDeliveryAnswer, "owner"); err != nil {
		t.Fatalf("answer deliver-merge: %v", err)
	}
	waitUntil(t, "the rewind the answer caused", func() bool {
		exec := tryReadExec(execPath)
		return exec != nil && exec.RewindStats.Count >= 1
	})

	// fix and verification run, then deliver is dispatched once more — the
	// re-dispatch that judges the redone work.
	waitUntil(t, "the deliver re-dispatch after the rewind round", func() bool {
		if sp.deliverReentryAt() >= 0 {
			return true
		}
		exec := tryReadExec(execPath)
		return exec != nil && exec.WorkflowStatus != StatusRunning
	})

	idx := sp.deliverReentryAt()
	if idx < 0 {
		exec := readExec(t, execPath)
		stopRun()
		t.Fatalf("REPORTED FAILURE: the deliver re-dispatch never ran; workflow=%s blocked_reason=%q",
			exec.WorkflowStatus, exec.BlockedReason)
	}
	// Let the re-dispatched step settle its verdict, so the rewind count
	// observed below belongs to it: the replay rewinds again, the healthy
	// re-entry either asks a new merge question or finishes.
	waitUntil(t, "the re-dispatched deliver's verdict", func() bool {
		exec := tryReadExec(execPath)
		if exec == nil {
			return false
		}
		if exec.RewindStats.Count >= 2 || exec.WorkflowStatus != StatusRunning {
			return true
		}
		st := exec.Steps[deliverStepID]
		return st != nil && st.PendingQuestion != nil
	})
	exec := readExec(t, execPath)
	stopRun()
	reentry := sp.at(idx)
	replayed := hasConsumedAnswer(reentry.Answers)
	if replayed || exec.RewindStats.Count != 1 {
		t.Fatalf("REPORTED FAILURE: the deliver re-dispatch after the fix/verification round carried the already-consumed back-to-code answer %q (resume=%v, %d answer(s), replayed=%v) and the workflow then held %d rewind(s) (want exactly 1: %+v) — the step re-applies a verdict the rewind already consumed and rewinds to fix again with nothing to do",
			consumedDeliveryAnswer, reentry.Resume, len(reentry.Answers), replayed,
			exec.RewindStats.Count, exec.RewindStats.Rewinds)
	}
	if len(exec.Steps[deliverStepID].Answers) != 1 {
		t.Fatalf("the human answer must stay archived on the step (audit), got %+v", exec.Steps[deliverStepID].Answers)
	}
	if exec.WorkflowStatus == StatusBlocked {
		t.Fatalf("a consumed answer must never park the work item blocked: %q", exec.BlockedReason)
	}
}

// TestResumedDeliverDispatchDropsConsumedAnswer pins the incident's literal
// shape: a deliver step re-dispatched as a RESUME (the 502 death of the
// reported sequence) whose answer chain was already closed by the gate FAIL
// rewind. Its spawn must not carry the consumed answer, or the resumed
// agent re-applies it as this round's verdict.
func TestResumedDeliverDispatchDropsConsumedAnswer(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("fix"), spec("verification"), gateSpec(deliverStepID, "fix"))
	mustInit(t, d)

	// The durable state after the answer round: the human's answer is
	// archived, the gate FAIL closed the chain (AnswerBase = len(Answers)),
	// and the step is in_progress awaiting its next dispatch (a resume,
	// as after the incident's gateway 502).
	d.mu.Lock()
	st := d.exec.Steps[deliverStepID]
	st.Status = capsule.StepInProgress
	st.Answers = []capsule.ArchivedAnswer{{
		QuestionID: "deliver-merge",
		Answer:     consumedDeliveryAnswer,
		AnsweredBy: "owner",
		AnsweredAt: "2026-10-28T02:04:15Z",
	}}
	st.AnswerBase = len(st.Answers)
	d.exec.CurrentStep = deliverStepID
	d.exec.WorkflowStatus = StatusRunning
	d.mu.Unlock()

	sp, _, err := d.prepareDispatch(deliverStepID)
	if err != nil {
		t.Fatalf("prepareDispatch: %v", err)
	}
	if !sp.Resume {
		t.Fatalf("setup: this is the resumed dispatch of the incident, got resume=%v", sp.Resume)
	}
	if hasConsumedAnswer(sp.Answers) {
		t.Fatalf("REPORTED FAILURE: a RESUMED deliver dispatch was handed the already-consumed back-to-code answer %q (answers: %+v) — the rewind's AnswerBase closed that chain, so the resumed agent re-applies the old verdict instead of judging this round",
			consumedDeliveryAnswer, sp.Answers)
	}
}
