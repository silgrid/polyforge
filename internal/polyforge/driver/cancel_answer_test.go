package driver

// cancel_answer_test.go: the reserved cancel answer — recognised once
// (trimmed, any case, the whole answer), archived verbatim, written as the
// durable cancel intent with the blocked park in the answer's own write —
// and every persisted question states it exactly once.

import (
	"strings"
	"testing"
)

func TestReservedCancelAnswerRecognition(t *testing.T) {
	for in, want := range map[string]bool{
		"cancel": true, "  Cancel\n": true, "CANCEL": true,
		"cancel it": false, "please cancel": false, "": false, "cancelled": false,
	} {
		if got := IsCancelAnswer(in); got != want {
			t.Errorf("IsCancelAnswer(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCancelAnswerParksBlockedWithIntent(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"), spec("b"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	resultAt(t, d, "a", 1, OutcomeNeedsInput, `{"question_id":"q1","question":"go on?"}`, nil)
	x := readExec(t, execPath)
	q := x.Steps["a"].PendingQuestion
	if q == nil || strings.Count(q.Question, CancelAnswerSentence) != 1 || !strings.HasPrefix(q.Question, "go on?") {
		t.Fatalf("the persisted question states the reserved answer once, after the step's own text: %+v", q)
	}
	if err := d.Answer("q1", "  Cancel ", "owner"); err != nil {
		t.Fatal(err)
	}
	x = readExec(t, execPath)
	if x.WorkflowStatus != StatusBlocked || x.CancelRequested == nil || x.CancelRequested.QuestionID != "q1" ||
		x.CancelRequested.Answer != "  Cancel " || x.CancelRequested.AnsweredBy != "owner" {
		t.Fatalf("the cancel answer parks blocked with the durable intent: %q %+v", x.WorkflowStatus, x.CancelRequested)
	}
	if a := x.Steps["a"].Answers; len(a) != 1 || a[0].Answer != "  Cancel " || x.Steps["a"].PendingQuestion != nil {
		t.Fatalf("the answer is archived verbatim and the question closed: %+v", x.Steps["a"])
	}
	if x.Steps["b"].Status != "pending" || len(x.RewindStats.Rewinds) != 0 {
		t.Fatal("a cancel answer routes nothing")
	}
}

func TestCancelAtReviewExitFabricatesNoRuling(t *testing.T) {
	d, execPath := exitLane(t)
	reviewFail(t, d, 5, twoOffGraph)
	answerExit(t, d, "cancel")
	x := readExec(t, execPath)
	if x.WorkflowStatus != StatusBlocked || x.CancelRequested == nil || len(x.Steps[reviewStepID].OwnerRulings) != 0 {
		t.Fatalf("cancel at the owner exit records no ruling: %q %+v", x.WorkflowStatus, x.Steps[reviewStepID].OwnerRulings)
	}
}

// Review s8#1: the owner's resume supersedes a cancel intent whose cancel
// never landed at the hub — the resumed workflow carries no stale intent
// its next blocked close would act on.
func TestOwnerResumeDropsCancelIntent(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"), spec("b"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	resultAt(t, d, "a", 1, OutcomeNeedsInput, `{"question_id":"q1","question":"go on?"}`, nil)
	if err := d.Answer("q1", "cancel", "owner"); err != nil {
		t.Fatal(err)
	}
	if x := readExec(t, execPath); x.CancelRequested == nil || x.WorkflowStatus != StatusBlocked {
		t.Fatalf("precondition: the cancel answer parks blocked with the intent: %+v", x.CancelRequested)
	}
	if err := d.ResumeCurrent(); err != nil {
		t.Fatal(err)
	}
	if x := readExec(t, execPath); x.WorkflowStatus != StatusRunning || x.CancelRequested != nil {
		t.Fatalf("the owner's resume drops the cancel intent: %q %+v", x.WorkflowStatus, x.CancelRequested)
	}
}
