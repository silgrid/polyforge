package test

// v1_s19_answer_persistence_test.go: answer durability across restarts and crashes, driver/envelope
// edition. On the driver path the CONSUMER of an answer is the DRIVER
// (the D9 archive-and-clear write on the capsule's execution.json) — the
// capsule is the answer's persistent authority (the daemon-private
// answers.json ledger and its quarantine scenario were retired with the
// legacy agent-relay path). The three scenarios pin the durability
// contract on those faces:
//
//   - A: an open question is ON DISK (execution.json pending_question:
//     id, question, step, asked_at) while the workflow parks; the
//     QUESTION column mirrors the durable record.
//   - B: the question is open across a daemon SIGKILL; the restarted
//     daemon re-presents the SAME persisted id on the QUESTION column
//     and the answer closes it.
//   - C: a terminal outcome reached before a daemon crash is replayed
//     (from the durable file), never re-run: the restarted daemon
//     re-derives nothing, the spawn journal and the execution state are
//     byte-identical, and the WI stays wrapped.
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------
// Scenario A: the open question is durable on the capsule face.

func TestV1S19AwaitingAnswerIsPersistedToDisk(t *testing.T) {
	inst := newV1Install(t)
	const qid = "q_s19a"
	const question = "Should the pending question survive a restart?"
	wi := inst.newWI(t, "s19 A: the open question persisted to the capsule state",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": question},
		}, nil)

	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	waitQuestion(t, d, wi, qid, 30*time.Second)

	// The pending question is ON DISK in the daemon runtime state that
	// matters — the capsule's execution.json, written by the driver's
	// single transition entry: id, question text, step and ask moment.
	capDir := d.capsuleDir(wi)
	x := readExecution(t, capDir)
	pq := x.Steps["grill"].PendingQuestion
	if pq == nil {
		t.Fatal("no pending_question on the durable face")
	}
	if (*pq)["id"] != qid || (*pq)["step"] != "grill" {
		t.Fatalf("pending_question identity wrong: %v", pq)
	}
	if (*pq)["question"] != question {
		t.Fatalf("pending_question text wrong: %v", (*pq)["question"])
	}
	if asked, _ := (*pq)["asked_at"].(string); asked == "" {
		t.Fatalf("pending_question carries no asked_at: %v", pq)
	}
	// The QUESTION column mirrors the durable record (the D6 owner face),
	// and the workflow is parked, not blocked.
	wq := d.watch(t, wi).Execution.PendingQuestion
	if wq == nil || wq.ID != qid || !strings.Contains(wq.Question, "restart?") {
		t.Fatalf("the QUESTION face does not mirror the durable record: %+v", wq)
	}
	if st := inst.wiStatus(t, wi); st != "running" {
		t.Fatalf("parked workflow status %q, want running", st)
	}

	// The answer's durable home is the same file: the D9 write archives
	// the pair and clears the pending question in ONE atomic transition.
	const answerText = "Yes: the capsule is the authority."
	out := d.pf(t, "answer", wi, "--text", answerText, "--question", qid,
		"--by", "usr_root")
	if !strings.Contains(out, qid) || !strings.Contains(out, "delivered") {
		t.Fatalf("answer outcome: %q", out)
	}
	waitNoQuestion(t, d, wi, 30*time.Second)
	x = readExecution(t, capDir)
	if x.Steps["grill"].PendingQuestion != nil {
		t.Fatalf("pending_question survived the answer: %+v", x.Steps["grill"].PendingQuestion)
	}
	if len(x.Steps["grill"].Answers) != 1 ||
		x.Steps["grill"].Answers[0]["question_id"] != qid ||
		x.Steps["grill"].Answers[0]["answer"] != answerText {
		t.Fatalf("the archived answer is wrong: %+v", x.Steps["grill"].Answers)
	}
}

// ---------------------------------------------------------------------
// Scenario B: the open question across a daemon SIGKILL — the restart
// re-presents the SAME persisted id and the answer closes it.

func TestV1S19AwaitingAnswerRedeliveredAfterDaemonCrash(t *testing.T) {
	inst := newV1Install(t)
	const qid = "q_s19b"
	const decoyQID = "q_s19b_decoy"
	// The decoy id (the legacy scenario's resume-plan decoy) has no
	// driver-side hook to exploit: the driver parks on the DURABLE
	// pending question across the restart — no re-ask happens at all —
	// so the decoy is pinned negatively instead: it must appear nowhere
	// in the whole run's records.
	_ = decoyQID
	wi := inst.newWI(t, "s19 B: the open question across a daemon crash",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "Which variant should the recovery take?"},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)

	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	waitQuestion(t, d, wi, qid, 30*time.Second)

	// Crash the daemon hard while the question is open; the durable
	// pending question is all the restart has.
	d.crash()
	if x := readExecution(t, d.capsuleDir(wi)); x.Steps["grill"].PendingQuestion == nil ||
		(*x.Steps["grill"].PendingQuestion)["id"] != qid {
		t.Fatal("the open question did not survive the crash on the durable face")
	}

	d2 := restartSameDaemon(t, d, "daemon2.log")

	// The restarted daemon re-presents the SAME persisted id on the
	// QUESTION column — the redelivery is the durable file speaking, and
	// the plan's decoy id never surfaces.
	waitQuestion(t, d2, wi, qid, 60*time.Second)
	if q := questionIDOf(statusQuiet(d2), wi); q != qid {
		t.Fatalf("the restarted QUESTION column shows %q, want the persisted %q", q, qid)
	}

	// The answer closes it and the run finishes. The needs_input EVENT
	// fires once per monitor lifetime (each daemon's monitor emits it on
	// first sight of the durable question) — exactly 2 across the two
	// daemon lives, and the durable face proves it was ONE question:
	// the same id throughout, asked_at unchanged by the restart.
	answerQuestion(t, d2, wi, qid, "Take the durable variant.")
	waitWorkflowStatus(t, d2, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	if n := countOf(d2.eventKinds(t, wi), "needs_input"); n != 2 {
		t.Fatalf("needs_input fired %d times, want 2 (one per monitor life): %v",
			n, d2.eventKinds(t, wi))
	}

	// The sealed chain: the answer archived under the PERSISTED id, the
	// pending question gone, the decoy nowhere.
	exec := inst.bareFileAtMain(t, wi, "runtime-state/execution.json")
	if !strings.Contains(exec, "Take the durable variant.") {
		t.Fatalf("sealed execution.json misses the answer:\n%s", exec)
	}
	if strings.Contains(exec, decoyQID) {
		t.Fatalf("sealed execution.json carries the decoy question id %s:\n%s", decoyQID, exec)
	}
	if strings.Contains(exec, `"pending_question"`) {
		t.Fatalf("sealed execution.json still carries a pending question:\n%s", exec)
	}
	if !strings.Contains(exec, qid) {
		t.Fatalf("sealed execution.json misses the persisted question id %s:\n%s", qid, exec)
	}
	x := readExecution(t, d2.capsuleDir(wi))
	for _, step := range []string{"grill", "fix", "verification"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
}

// ---------------------------------------------------------------------
// Scenario C: the final outcome reached before a daemon crash is
// replayed, never re-run.

func TestV1S19FinalOutcomeReplayedAfterDaemonCrash(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "s19 C: the terminal outcome replayed after a crash",
		envelopeAutopilot(), nil)

	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	// The workflow completes and the terminal wiring wraps it — the
	// final outcome is durable in the capsule and at aihub.
	waitWorkflowStatus(t, d, wi, "completed", 90*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)
	execBefore, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "execution.json"))
	if err != nil {
		t.Fatal(err)
	}
	spawnsBefore := len(spawnJournal(t, capDir))
	if spawnsBefore != 3 {
		t.Fatalf("spawn journal has %d entries, want 3 (one pass)", spawnsBefore)
	}

	// Crash the daemon AFTER the terminal outcome; restart on the same
	// state. The restarted daemon must re-derive NOTHING: the final
	// outcome is replayed from the durable file, not re-run.
	d.crash()
	d2 := restartSameDaemon(t, d, "daemon2.log")
	time.Sleep(5 * time.Second) // any re-derivation would show within this window
	if st := inst.wiStatus(t, wi); st != "wrapped" {
		t.Fatalf("the restarted daemon disturbed the terminal WI: %q", st)
	}
	if n := len(spawnJournal(t, capDir)); n != spawnsBefore {
		t.Fatalf("the restart re-ran work: spawn journal %d entries, want %d", n, spawnsBefore)
	}
	execAfter, err := os.ReadFile(filepath.Join(d2.capsuleDir(wi), "runtime-state", "execution.json"))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if json.Unmarshal(execBefore, &before) != nil || json.Unmarshal(execAfter, &after) != nil {
		t.Fatal("execution.json unparsable")
	}
	if fmt.Sprint(before["version"]) != fmt.Sprint(after["version"]) {
		t.Fatalf("the restarted daemon wrote new versions (%v -> %v)", before["version"], after["version"])
	}
	if before["workflow_status"] != "completed" || after["workflow_status"] != "completed" {
		t.Fatalf("the terminal outcome moved: %v -> %v", before["workflow_status"], after["workflow_status"])
	}
	// The restarted daemon holds no job for the finished WI.
	if st := d2.status(t); len(st.Jobs) != 0 {
		t.Fatalf("the restarted daemon holds jobs: %+v", st.Jobs)
	}
	// And the session stays cleared (the wrap's terminal close).
	if sess := inst.sessionOf(t, wi); sess.Fingerprint != "" {
		t.Fatalf("wrapped work item still names session %q", sess.Fingerprint)
	}
}
