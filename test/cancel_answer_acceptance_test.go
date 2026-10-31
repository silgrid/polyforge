package test

// cancel_answer_acceptance_test.go: a plain adopter's walk — a lane step
// asks a question, the owner answers `cancel` with `pf answer`, and the
// work item ends in the hub's existing canceled status through the
// blocked→canceled door: no wrap, and `pf watch` shows the existing
// canceled class. The question itself states the reserved answer.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCancelAnswerAcceptance(t *testing.T) {
	const qid = "scope_question"
	plan := []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "needs_input", "question_id": qid,
			"question": "The ticket names two scopes; which one?"},
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
	}
	d, inst, wi, capDir := runAcceptanceLane(t, "a chore whose owner abandons it at a question", "chore", plan)

	waitQuestion(t, d, wi, qid, 60*time.Second)
	q := pendingQuestion(t, capDir)
	if text, _ := q["question"].(string); !strings.Contains(text, "Answer `cancel`") {
		t.Fatalf("the open question must state the reserved cancel answer: %q", text)
	}
	answerQuestion(t, d, wi, qid, "Cancel")

	inst.waitWIStatus(t, wi, "canceled", 60*time.Second)
	if n := stepDispatches(t, capDir, "plan"); n != 1 {
		t.Errorf("the cancel answer re-dispatched plan (%d dispatches)", n)
	}
	for _, kind := range d.eventKinds(t, wi) {
		if strings.Contains(kind, "wrap") {
			t.Errorf("a canceled work item emitted %q", kind)
		}
	}
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "blocked" {
		t.Errorf("the capsule keeps the closed vocabulary (blocked + intent), got %q", x.WorkflowStatus)
	}
	out := d.pf(t, "watch", wi, "--once", "--json")
	var snap map[string]any
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("watch --json: %v (%s)", err, out)
	}
	if !strings.Contains(out, "canceled") {
		t.Errorf("pf watch must present the existing canceled class: %s", out)
	}
}
