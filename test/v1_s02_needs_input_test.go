package test

import (
	"strings"
	"testing"
	"time"
)

// v1_s02: the needs_input full loop, driver/envelope edition. The grill
// step's envelope parks the driver on an open question (persisted in
// execution.json in the same atomic write that parked the step), the
// question surfaces on the ops status QUESTION column (the D6 owner
// face, derived from the durable file), the human answers through the
// real CLI, the DRIVER consumes the answer (the D9 archive-and-clear
// write — no agent ack anywhere), the step is re-dispatched in place
// with its archived Q&A history, and the workflow completes and wraps.
// Every leg is asserted on the driver faces.
func TestV1S02NeedsInputLoop(t *testing.T) {
	inst := newV1Install(t)
	const qid = "q_v1s02"
	const answerText = "Take the lean variant."
	wi := inst.newWI(t, "s02: needs_input round trip",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "Which design should the code step take: lean or compatible?"},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)

	// The QUESTION column face: the pending question's id and text
	// surface through the status/ops faces derived from the durable
	// execution.json.
	waitQuestion(t, d, wi, qid, 30*time.Second)
	pq := d.watch(t, wi).Execution.PendingQuestion
	if pq == nil || !strings.Contains(pq.Question, "lean or compatible") {
		t.Fatalf("pending question text wrong: %+v", pq)
	}
	ops := d.pf(t, "ops", "status")
	if !strings.Contains(ops, qid) {
		t.Fatalf("pf ops status QUESTION column: %q does not carry %s", ops, qid)
	}

	// The question is ON DISK before it is answerable: the pending
	// question rides the same atomic write that parked the step.
	capDir := d.capsuleDir(wi)
	x := readExecution(t, capDir)
	if pq := x.Steps["grill"].PendingQuestion; pq == nil || (*pq)["id"] != qid {
		t.Fatalf("pending_question not persisted on the execution.json face: %+v", x.Steps["grill"])
	}
	if (*x.Steps["grill"]).Status != "in_progress" {
		t.Fatalf("the asking step is not parked in_progress: %+v", x.Steps["grill"])
	}

	// The human answers through the real CLI; the driver archives the
	// pair and clears pending_question in ONE atomic write.
	answerQuestion(t, d, wi, qid, answerText)

	// The question closes and the loop continues to its terminal state.
	waitNoQuestion(t, d, wi, 30*time.Second)
	waitWorkflowStatus(t, d, wi, "completed", 90*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)

	// The consumption contract: the answer is archived in execution.json
	// (same atomic write that cleared pending_question), and the sealed
	// chain carries it server-side.
	exec := inst.bareFileAtMain(t, wi, "runtime-state/execution.json")
	if !strings.Contains(exec, answerText) {
		t.Fatalf("sealed execution.json misses the answer:\n%s", exec)
	}
	if strings.Contains(exec, `"pending_question"`) {
		t.Fatalf("sealed execution.json still carries a pending question:\n%s", exec)
	}
	if !strings.Contains(exec, `"fix"`) || !strings.Contains(exec, `"done"`) {
		t.Fatalf("the workflow did not continue past the question:\n%s", exec)
	}
	// The archived answer carries the full relay shape.
	x = readExecution(t, capDir)
	grill := x.Steps["grill"]
	if len(grill.Answers) != 1 || grill.Answers[0]["question_id"] != qid ||
		grill.Answers[0]["answer"] != answerText || grill.Answers[0]["answered_by"] != "usr_root" {
		t.Fatalf("archived answer wrong: %+v", grill.Answers)
	}
	// The re-dispatch face: grill was answered and COMPLETED by its
	// second derivation (the envelope plan's second grill action), then
	// the workflow advanced — every step done.
	for _, step := range []string{"grill", "fix", "verification"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	env2 := archivedEnvelope(t, capDir, "grill", 2)
	if env2["outcome"] != "completed" {
		t.Fatalf("grill's answer-carrying re-dispatch envelope wrong: %v", env2["outcome"])
	}

	// The daemon events tell the loop: the monitor's needs_input event
	// fired on first sight of the question (the driver writes it; the
	// monitor classifies it). The driver lane's answer consumption is
	// daemon-side — there is no agent-ack event to wait for.
	kinds := d.eventKinds(t, wi)
	if !contains(kinds, "needs_input") {
		t.Fatalf("events miss the needs_input leg: %v", kinds)
	}
}
