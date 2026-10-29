package test

import (
	"testing"
	"time"
)

// v1_s17: a SIGKILL landing AFTER the envelope write, driver/envelope
// edition (the fakepi crash-after-write shape). The fix step's process
// writes its result envelope to the drop path and dies immediately — the
// written envelope must still be collected EXACTLY ONCE: the driver's
// collection polls the drop path independently of the process's
// liveness, so the crash after the write is teardown, never a death —
// no recovery fires, no re-derivation happens, and the workflow advances
// past the crashed process as if it had exited politely.
func TestV1S17SealCrashResume(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "s17: crash after the envelope write, collected exactly once",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			// The crash-after-write shape: the envelope lands, then the
			// process dies (exit 101) before the driver's teardown.
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "crash": "after"},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	capDir := d.capsuleDir(wi)

	// The crash happened on cue (the step process journaled it).
	waitUntil(t, 30*time.Second, "the after-write crash was never journaled", func() bool {
		for _, rec := range fakeJournal(t, capDir) {
			if rec["type"] == "crash_after" && rec["step"] == "fix" {
				return true
			}
		}
		return false
	})

	// The written envelope was collected exactly once and the workflow
	// advanced: fix is done with exactly one accepted attempt (a second
	// collection would be the duplicate-result refusal), and no recovery
	// signal ever fired for the crash.
	waitStepStatus(t, d, wi, "fix", "done", 30*time.Second)
	x := readExecution(t, capDir)
	accepted := 0
	for _, a := range x.Steps["fix"].Attempts {
		if a["cause"] == "accepted" {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("fix carries %d accepted attempts, want 1 (collected exactly once): %+v", accepted, x.Steps["fix"].Attempts)
	}
	env2 := archivedEnvelope(t, capDir, "fix", 2)
	if env2["outcome"] != "completed" {
		t.Fatalf("the crashed process's envelope was not archived: %v", env2)
	}
	if n := countOf(d.eventKinds(t, wi), "needs_recovery"); n != 0 {
		t.Fatalf("needs_recovery fired %d times pre-completion (the crash-after-write is teardown, not death): %v",
			n, d.eventKinds(t, wi))
	}

	// The workflow ran to its terminal wrap on the surviving dispatches.
	waitWorkflowStatus(t, d, wi, "completed", 90*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)

	// The spawn journal holds exactly the three step dispatches (no
	// re-derivation), the whole chain is done, and the only recovery
	// signal in the events log is the completion translation.
	spawns := waitSpawnCount(t, d, wi, 3, 30*time.Second)
	if len(spawns) != 3 {
		t.Fatalf("spawn journal has %d entries, want 3 (no re-derivation): %+v", len(spawns), spawns)
	}
	x = readExecution(t, capDir)
	for _, step := range []string{"grill", "fix", "verification"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	kinds := d.eventKinds(t, wi)
	if n := countOf(kinds, "needs_recovery"); n != 0 {
		t.Fatalf("needs_recovery fired %d times, want 0 (the completion is no death): %v", n, kinds)
	}
	waitUntil(t, 15*time.Second, "processes survived the episode", func() bool {
		return len(orphanPIDs(wi)) == 0
	})
}
