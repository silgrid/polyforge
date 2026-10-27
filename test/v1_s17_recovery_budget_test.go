package test

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// v1_s17: the retry budget runs OUT, driver/envelope edition (v2-converge
// 2.3). The live step process is SIGKILLed and every re-dispatch dies AT
// BIRTH (fail_spawn_resume / fail_spawn_fresh: each derivation exits before
// reading one byte of stdin, journaled as spawn_fail). Every dead
// dispatch is a counted failure of the step — exit_other, same-model
// retry: the first retry resumes the transcript, the second starts fresh
// — and the third trips the per-step failure counter: the driver parks
// the workflow blocked (blocked event, durable blocked_reason) and the
// runtime's terminal wiring hands the work item to its owner —
// RTFinished(blocked), the aihub side blocked with waiting_on=owner and
// NO closed_at (blocked is resumable-by-owner, not closed), the job
// dropped from the daemon's live set. The DRIVER-crash bound (the
// recovery engine's consecutive-failures ledger) has its own scenario
// (TestV2DriverCrashLoopBoundedThenBlocked). This one runs the counter
// through the REAL daemon, the real spawner and real step processes.
func TestV1S17RecoveryBudgetBoundedFail(t *testing.T) {
	inst := newV1Install(t)
	actions := []map[string]any{
		// Ordinal 1 is the healthy original: it burns a 2.5s delay (the
		// kill window) before its envelope write.
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true, "ms": 2500},
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	}
	wi := inst.newWI(t, "s17: every re-derivation fails at birth", actions, nil)
	// ONE plan for the whole run: the original derivation executes the
	// actions above, and every derivation the recovery ladder mints
	// afterwards dies at birth (resume AND fresh re-derivations alike).
	writePlanExtra(t, inst, wi, actions, nil, map[string]any{
		"fail_spawn_resume": true,
		"fail_spawn_fresh":  true,
	})

	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	// The original derivation is healthy (live, mid-delay), then killed.
	capDir := d.capsuleDir(wi)
	waitSpawnCount(t, d, wi, 1, 30*time.Second)
	waitSpawnTranscript(t, d, wi, 1, 30*time.Second)
	pid := latestSpawnPID(t, d, wi)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 %d: %v", pid, err)
	}

	// The counter: two failed retries, then the third counted failure
	// parks the WI. The terminal state is BLOCKED.
	inst.waitWIStatus(t, wi, "blocked", 120*time.Second)

	// blocked is waiting-on-owner, NOT closed: the owner can still
	// resume it (that is the whole difference from a terminal failure).
	item, err := inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("get work item: %v", err)
	}
	if item.WaitingOn == nil || *item.WaitingOn != "owner" {
		t.Fatalf("blocked work item waiting_on = %v, want \"owner\"", item.WaitingOn)
	}
	if item.ClosedAt != nil {
		t.Fatalf("blocked work item must not be closed, got closed_at %q", *item.ClosedAt)
	}

	// T5 (M2): the bounded fail CLEARED the session row — blocked with a
	// live session would be revivable by a restart and the ledger's
	// bound would be a lie. The cleared row keeps the work item OUT of the
	// claimable set: the CAS gate answers the typed NOT_CLAIMABLE refusal
	// on the wire.
	if sess := inst.sessionOf(t, wi); sess.Fingerprint != "" {
		t.Fatalf("blocked work item still names session %q (M2: bounded fail must clear the session)", sess.Fingerprint)
	}
	refuseClaim(t, inst, wi, "NOT_CLAIMABLE")

	// The daemon dropped the job: no relaunch storm, nothing live.
	waitUntil(t, 30*time.Second, "the bounded-failed job never left the daemon's live set", func() bool {
		for _, j := range d.status(t).Jobs {
			if j.WorkItemID == wi {
				return false
			}
		}
		return true
	})

	// THREE failure records (the kill plus two birth-deaths), same-model,
	// counted 1/2/3, and the blocked decision on the third; ONE recovery
	// signal — the translation of the blocked exit — and no re-derivation.
	fails := stepFailures(t, d, wi)
	if len(fails) != 3 {
		t.Fatalf("step_failed records %+v, want 3 (the kill + two failed dispatches)", fails)
	}
	for i, f := range fails {
		if f.Cause != "exit_other" || f.Count != i+1 {
			t.Fatalf("failure %d = %+v, want exit_other counted %d", i, f, i+1)
		}
		wantDisp := "retry_same_model"
		if i == 2 {
			wantDisp = "blocked"
		}
		if f.Disposition != wantDisp {
			t.Fatalf("failure %d disposition %q, want %q", i, f.Disposition, wantDisp)
		}
	}
	// the 1st retry resumes, the 2nd starts fresh
	if fails[0].Next != "resume" || fails[1].Next != "fresh" {
		t.Fatalf("retry shapes %q/%q, want resume then fresh", fails[0].Next, fails[1].Next)
	}
	kinds := d.eventKinds(t, wi)
	if n := countOf(kinds, "blocked"); n != 1 {
		t.Fatalf("blocked events %d, want 1: %v", n, kinds)
	}
	if n := countOf(kinds, "needs_recovery"); n != 1 {
		t.Fatalf("needs_recovery fired %d times, want 1 (the blocked-exit translation): %v", n, kinds)
	}
	if n := countOf(kinds, "wi_agent_rederived"); n != 0 {
		t.Fatalf("re-derivation count %d, want 0 (the counter, not the recovery ladder, bounded it): %v", n, kinds)
	}

	// The spawn journal pins the dispatch shape. Failed derivations ARE
	// journaled — the parent writes the identity line before the prompt —
	// so the file carries the original plus the two birth-death retries
	// (resume, then fresh), counters advancing.
	spawns := spawnJournal(t, capDir)
	if len(spawns) != 3 {
		t.Fatalf("spawn journal has %d entries, want 3 (original + two failed retries): %+v", len(spawns), spawns)
	}
	wantResume := []bool{false, true, false}
	for i, want := range wantResume {
		if got, _ := spawns[i]["resume"].(bool); got != want {
			t.Fatalf("spawn %d resume=%v, want %v (shape %+v)", i, got, want, spawns)
		}
		if n, _ := spawns[i]["spawn_n"].(float64); int(n) != i+1 {
			t.Fatalf("spawn %d carries spawn_n %v, want %d", i, spawns[i]["spawn_n"], i+1)
		}
	}
	// The agent side agrees it died at birth every time: the resume and
	// the fresh retry, and no second live "spawn" line.
	failBirths, live := 0, 0
	for _, r := range fakeJournal(t, capDir) {
		switch r["type"] {
		case "spawn_fail":
			failBirths++
		case "spawn":
			live++
		}
	}
	if failBirths != 2 || live != 1 {
		t.Fatalf("must-fail births: %d, %d live derivations, want 2 + 1", failBirths, live)
	}

	// The interrupted step is still in_progress on the durable face and
	// the workflow is parked blocked with its reason (the owner's §6.4
	// move: continue / abandon).
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "blocked" || x.Steps["grill"].Status != "in_progress" ||
		!strings.Contains(x.BlockedReason, "failed 3 times") {
		t.Fatalf("the durable face: status=%s grill=%s reason=%q",
			x.WorkflowStatus, x.Steps["grill"].Status, x.BlockedReason)
	}
	if sup := x.Steps["grill"].Supervision; sup == nil || sup["failure_count"] != float64(3) {
		t.Fatalf("the failure counter must be persisted: %+v", sup)
	}
}
