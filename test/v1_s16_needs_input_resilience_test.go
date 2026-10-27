package test

// v1_s16: F9 — the needs_input question/answer protocol's resilience,
// driver/envelope edition. The legacy suite proved the protocol's
// fractures on the WI-Agent RPC face (pf/answer control relays, the
// flush proxy, agent-side acks — all since deleted); every one of those faces is re-expressed
// here on the driver's answer path, where the CONSUMER of an answer is
// the DRIVER itself (the D9 archive-and-clear write on execution.json —
// there is no agent ack anywhere), the pending question's durable home
// is the capsule's execution.json, and the question round-trip rides the
// QUESTION column + `pf answer` through the real CLI.
//
// Scenarios:
//
//   - A: the answer is consumed durably (driver-side), the daemon is
//     kill -9ed mid-continuation, and the restarted daemon's resumed
//     driver continues the workflow FROM the archived answer — the
//     answer survives the crash because the capsule, not any agent
//     memory, is its persistent authority.
//   - B: a needs_input envelope with an EMPTY question_id (scripted
//     verbatim) is refused by the driver's transition — fail-closed: no
//     unanswerable pending is ever minted — and the workflow recovers
//     through the recovery engine onto the well-formed question.
//   - C: the same drop path written twice (the duplicate envelope
//     shape) is consumed exactly once: one pending, one needs_input,
//     one accepted attempt.
//   - D: the zero-landing half: an answer accepted while the step
//     process is provably dead to inputs — the DRIVER consumed it, no
//     pf/answer ever reached any agent — survives a daemon kill -9 and
//     is replayed from the durable file by the successor.
import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Scenario A: the durable answer across a daemon kill -9.

func TestV1S16AnswerAutoRedeliveryAfterKill9(t *testing.T) {
	inst := newV1Install(t)
	const qid = "q_f9a"
	const answerText = "Take the resilient variant."
	// Ordinals: 1 grill asks; 2 grill completes with the answer; 3 fix
	// (delayed write — the kill window); 4 fix again (the post-restart
	// resume dispatch); 5-6 the tail.
	wi := inst.newWI(t, "s16 A: the durable answer across a daemon kill -9",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "Which variant should the recovery take?"},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 3000},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	waitQuestion(t, d, wi, qid, 30*time.Second)

	// The owner answers. The DRIVER consumes it — the D9 write archives
	// the pair and clears the pending question in one atomic transition,
	// then re-dispatches the step in place with its Q&A history.
	answerQuestion(t, d, wi, qid, answerText)
	waitNoQuestion(t, d, wi, 30*time.Second)
	capDir := d.capsuleDir(wi)
	waitStepStatus(t, d, wi, "grill", "done", 30*time.Second)
	x := readExecution(t, capDir)
	if len(x.Steps["grill"].Answers) != 1 || x.Steps["grill"].Answers[0]["answer"] != answerText {
		t.Fatalf("the answer was never archived: %+v", x.Steps["grill"].Answers)
	}
	// The re-dispatch (grill's second derivation) is live and fix is
	// already in progress when the daemon dies mid-continuation.
	waitSpawnCount(t, d, wi, 3, 30*time.Second)
	waitStepStatus(t, d, wi, "fix", "in_progress", 30*time.Second)

	// Kill -9 the daemon mid-fix. The parked step process survives the
	// crash (the crash-safety property); execution.json — with the
	// archived answer — is the only memory either daemon needs.
	d.crash()
	if x := readExecution(t, capDir); len(x.Steps["grill"].Answers) != 1 {
		t.Fatal("the archived answer did not survive the daemon crash")
	}

	d2 := restartSameDaemon(t, d, "daemon2.log")
	// The restarted driver seeds past every used ordinal, re-dispatches
	// fix (resume, with the archived answers in its spawn), and finishes.
	waitUntil(t, 60*time.Second, "restarted daemon never re-dispatched fix", func() bool {
		return len(spawnJournal(t, capDir)) >= 4
	})
	waitWorkflowStatus(t, d2, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)

	// The answer round-trip closed exactly once, pre-crash, and its
	// record is in the sealed chain; the question never re-opened.
	kinds := d2.eventKinds(t, wi)
	if n := countOf(kinds, "needs_input"); n != 1 {
		t.Fatalf("needs_input fired %d times, want 1: %v", n, kinds)
	}
	exec := inst.bareFileAtMain(t, wi, "runtime-state/execution.json")
	if !strings.Contains(exec, answerText) {
		t.Fatalf("sealed execution.json misses the answer:\n%s", exec)
	}
	if strings.Contains(exec, `"pending_question"`) {
		t.Fatalf("sealed execution.json still carries a pending question:\n%s", exec)
	}
	x = readExecution(t, capDir)
	for _, step := range []string{"grill", "fix", "verification"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	// The crash survivor is a known orphan (a driver WI's step processes
	// have no daemon-side pid for the six-layer cleanup to signal — the
	// harness's own sweep collects them at teardown); nothing else leaks.
	waitUntil(t, 15*time.Second, "more than the one crash survivor leaked", func() bool {
		return len(orphanPIDs(wi)) <= 1
	})
}

// ---------------------------------------------------------------------------
// Scenario B: the empty question_id is refused fail-closed by the
// driver's needs_input transition.

func TestV1S16EmptyQuestionIDRefused(t *testing.T) {
	inst := newV1Install(t)
	const goodQID = "q_f9b_live"
	// Ordinal 1 is the MALFORMED shape: a needs_input envelope whose
	// question_id is scripted empty verbatim (the driver refuses a
	// needs_input result without question_id/question — the transition
	// errors, the driver exits, and the recovery engine re-dispatches
	// onto ordinal 2, the well-formed question).
	wi := inst.newWI(t, "s16 B: empty question id fail-closed",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": "",
				"question": "the malformed ask (must be refused)"},
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": goodQID,
				"question": "the well-formed ask"},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	capDir := d.capsuleDir(wi)

	// The refusal is visible: the driver's envelope_rejected event (it
	// lands in the durable per-WI event log) names the missing question
	// text verbatim.
	rej := waitForEvent(t, d, wi, "envelope_rejected", 60*time.Second)
	if !strings.Contains(string(rej.Body), "needs_input result is missing question_id") {
		t.Fatalf("the rejection must name the missing question id: %s", rej.Body)
	}

	// Fail-closed, not fail-deaf: no pending was ever minted off the
	// malformed envelope — the QUESTION column shows only the
	// well-formed follow-up (the recovery re-dispatch parked there).
	waitQuestion(t, d, wi, goodQID, 60*time.Second)
	// the needs_input event is written asynchronously (observe), so it may
	// land after the pending question is visible
	waitUntil(t, 10*time.Second, "the needs_input event never landed", func() bool {
		return countOf(d.eventKinds(t, wi), "needs_input") >= 1
	})
	if n := countOf(d.eventKinds(t, wi), "needs_input"); n != 1 {
		t.Fatalf("needs_input fired %d times, want 1 (only the well-formed question): %v",
			n, d.eventKinds(t, wi))
	}
	// The refused envelope itself is on the archived-envelope face —
	// the audit trail of what was refused. (The archive drops the empty
	// question_id field — omitempty on the envelope struct — so absence
	// and emptiness are the same refused shape; the question text rode
	// verbatim.)
	env := archivedEnvelope(t, capDir, "grill", 1)
	if env["outcome"] != "needs_input" {
		t.Fatalf("the refused envelope must be archived: %v", env)
	}
	if q, ok := env["question_id"]; ok && q != "" {
		t.Fatalf("the refused envelope must carry no question id: %v", env)
	}
	if env["question"] != "the malformed ask (must be refused)" {
		t.Fatalf("the refused envelope's question text must ride verbatim: %v", env["question"])
	}
	// The live attempt's own well-formed question is answerable, and the
	// loop finishes normally.
	answerQuestion(t, d, wi, goodQID, "The well-formed variant.")
	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	x := readExecution(t, capDir)
	if pq := x.Steps["grill"].PendingQuestion; pq != nil {
		t.Fatalf("pending question survived the answer: %+v", pq)
	}
	if len(x.Steps["grill"].Answers) != 1 || x.Steps["grill"].Answers[0]["question_id"] != goodQID {
		t.Fatalf("the well-formed answer was not archived: %+v", x.Steps["grill"].Answers)
	}
	// The refusal cost exactly one re-dispatch: five derivations, in plan
	// order, and the sealed chain carries the whole story.
	if n := len(spawnJournal(t, capDir)); n != 5 {
		t.Fatalf("spawn journal has %d entries, want 5 (the refused dispatch consumed ordinal 1)", n)
	}
}

// ---------------------------------------------------------------------------
// Scenario C: the same drop path written twice is consumed exactly once.

func TestV1S16DuplicateDropSameIDIsIdempotent(t *testing.T) {
	inst := newV1Install(t)
	const qid = "q_f9c"
	wi := inst.newWI(t, "s16 C: duplicate drop-path write is consumed once",
		[]map[string]any{
			// The duplicate shape: the IDENTICAL envelope (same result_id)
			// written twice back-to-back to the same drop path.
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "Which shape: one pending or two?", "dup": 2},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)

	// Exactly one pending question, with its text intact.
	waitQuestion(t, d, wi, qid, 30*time.Second)
	pq := d.watch(t, wi).Execution.PendingQuestion
	if pq == nil || pq.ID != qid || !strings.Contains(pq.Question, "one pending or two") {
		t.Fatalf("the duplicate write disturbed the pending: %+v", pq)
	}
	// the needs_input event is written asynchronously (observe), so it may
	// land after the pending question is visible
	waitUntil(t, 10*time.Second, "the needs_input event never landed", func() bool {
		return countOf(d.eventKinds(t, wi), "needs_input") >= 1
	})
	if n := countOf(d.eventKinds(t, wi), "needs_input"); n != 1 {
		t.Fatalf("needs_input fired %d times, want 1 (the duplicate drop is consumed once)", n)
	}
	capDir := d.capsuleDir(wi)
	// The fake journal saw the duplicate write...
	var dupRec map[string]any
	for _, rec := range fakeJournal(t, capDir) {
		if rec["type"] == "envelope" && rec["step"] == "grill" {
			if n, _ := rec["dup"].(float64); n == 2 {
				dupRec = rec
			}
		}
	}
	if dupRec == nil {
		t.Fatal("the duplicate envelope write never happened (dup=2 not journaled)")
	}
	// ...and the driver archived it exactly once. The accepted-attempt
	// count is checked after the completion (an acceptance lands with the
	// completed envelope; a second collection of the same result_id would
	// be the duplicate-result refusal, never a second question).
	env := archivedEnvelope(t, capDir, "grill", 1)
	if env["question_id"] != qid {
		t.Fatalf("archived envelope question wrong: %v", env["question_id"])
	}

	// The loop still finishes normally on the single pending.
	answerQuestion(t, d, wi, qid, "Exactly one pending.")
	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	x := readExecution(t, capDir)
	attempts := x.Steps["grill"].Attempts
	accepted := 0
	for _, a := range attempts {
		if a["cause"] == "accepted" {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("grill carries %d accepted attempts, want 1: %+v", accepted, attempts)
	}
	exec := inst.bareFileAtMain(t, wi, "runtime-state/execution.json")
	if strings.Contains(exec, `"pending_question"`) {
		t.Fatalf("sealed execution.json still carries a pending question:\n%s", exec)
	}
}

// ---------------------------------------------------------------------------
// Scenario D: the zero-landing answer — accepted while the step process
// is dead to inputs — survives a daemon kill -9 and is replayed by the
// successor from the durable file.

func TestV1S16ZeroLandingAnswerSurvivesDaemonCrash(t *testing.T) {
	inst := newV1Install(t)
	const qid = "q_f9d"
	const answerText = "Yes: the capsule is the crash's only memory of it."
	wi := inst.newWI(t, "s16 D: zero-landing answer across a daemon crash",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "Can an answer that never landed survive a daemon crash?"},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 3000},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)

	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	waitQuestion(t, d, wi, qid, 30*time.Second)

	// Zero landings by construction: the DRIVER consumes the answer
	// daemon-side; the parked step process's journal stays silent no
	// matter what the timing is. The CLI reports the question delivered.
	out := d.pf(t, "answer", wi, "--text", answerText, "--question", qid,
		"--by", "usr_root")
	if !strings.Contains(out, qid) || !strings.Contains(out, "delivered") {
		t.Fatalf("answer outcome: %q", out)
	}
	capDir := d.capsuleDir(wi)

	// The consumption is DURABLE before anything else moves: the archived
	// answer and the cleared pending are in execution.json — the capsule
	// is the driver path's answer authority (the daemon-private ledger
	// belongs to the legacy agent-relay path).
	waitNoQuestion(t, d, wi, 30*time.Second)
	waitStepStatus(t, d, wi, "grill", "done", 30*time.Second)
	x := readExecution(t, capDir)
	if len(x.Steps["grill"].Answers) != 1 || x.Steps["grill"].Answers[0]["answer"] != answerText {
		t.Fatalf("the accepted answer never reached the capsule's durable state: %+v", x.Steps["grill"].Answers)
	}

	// Let the continuation reach fix (its 3s delayed write is the kill
	// window), then kill -9 the daemon.
	waitSpawnCount(t, d, wi, 3, 30*time.Second)
	waitStepStatus(t, d, wi, "fix", "in_progress", 30*time.Second)
	d.crash()

	// The answer survived the crash — the capsule remembers.
	if x := readExecution(t, capDir); len(x.Steps["grill"].Answers) != 1 {
		t.Fatal("the answer did not survive the daemon crash")
	}

	// Restart the SAME daemon: the startup reconciliation recovers the
	// running work item, the resumed driver seeds past every used
	// ordinal and finishes the workflow from the durable state.
	d2 := restartSameDaemon(t, d, "daemon2.log")
	waitUntil(t, 60*time.Second, "restarted daemon never re-dispatched fix", func() bool {
		return len(spawnJournal(t, capDir)) >= 4
	})
	waitWorkflowStatus(t, d2, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)

	// The replayed state: the answer is in the sealed chain exactly once,
	// the pending never re-opened, exactly one needs_input across both
	// daemon lives, and still zero agent-side answer landings.
	exec := inst.bareFileAtMain(t, wi, "runtime-state/execution.json")
	if !strings.Contains(exec, answerText) {
		t.Fatalf("sealed execution.json misses the answer:\n%s", exec)
	}
	if strings.Contains(exec, `"pending_question"`) {
		t.Fatalf("sealed execution.json still carries a pending question:\n%s", exec)
	}
	if n := countOf(d2.eventKinds(t, wi), "needs_input"); n != 1 {
		t.Fatalf("needs_input fired %d times, want 1: %v", n, d2.eventKinds(t, wi))
	}
	x = readExecution(t, capDir)
	for _, step := range []string{"grill", "fix", "verification"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	// The known crash survivor is the only orphan allowed (see scenario A).
	waitUntil(t, 15*time.Second, "more than the one crash survivor leaked", func() bool {
		return len(orphanPIDs(wi)) <= 1
	})
}
