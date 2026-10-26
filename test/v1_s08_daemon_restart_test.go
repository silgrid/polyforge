package test

import (
	"path/filepath"
	"testing"
	"time"
)

// v1_s08: daemon crash with a running driver workflow. The daemon process
// is SIGKILLed mid-step (the parked step process deliberately survives —
// it parks after its envelope write). The restarted daemon runs the §十一
// reconciliation against aihub's own verdict — the compare-only session
// verify (B3) says the crashed daemon's session still holds it, status
// running — routes the job through the recovery engine, and the restarted
// driver resumes PAST every spawn ordinal the predecessor used (the spawn
// journal is the fence's source of truth), finishing the work item inside
// the SAME session (T4: the fingerprint never moves).
func TestV1S08DaemonRestart(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "s08: daemon crash mid-run",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			// Ordinal 2 (fix) burns a 3s delay — the deterministic crash window.
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 3000},
			// Ordinal 3: the post-restart resume dispatch of fix.
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	waitSpawnCount(t, d, wi, 2, 30*time.Second)
	waitStepStatus(t, d, wi, "fix", "in_progress", 30*time.Second)
	capDir := d.capsuleDir(wi)
	t.Cleanup(func() { killOrphans(wi) })

	// The session fingerprint BEFORE the crash — T4's identity contract.
	sessBefore := inst.sessionOf(t, wi)
	if sessBefore.Fingerprint == "" {
		t.Fatal("running work item carries no session")
	}

	// Crash the daemon hard. The parked step process survives (the
	// crash-safety property the orphan assertion below pins).
	survivorPID := latestSpawnPID(t, d, wi)
	d.crash()
	waitUntil(t, 10*time.Second, "the step process did not survive the daemon crash", func() bool {
		pids := orphanPIDs(wi)
		for _, p := range pids {
			if p == survivorPID {
				return true
			}
		}
		return false
	})

	// Restart the SAME daemon (same socket, data dir, workspace). The
	// startup reconciliation recovers the running work item.
	d2 := &v1Daemon{
		t: t, inst: inst, sock: d.sock, dataDir: d.dataDir, workspace: d.workspace,
		machineID: d.machineID, extraArgs: d.extraArgs,
		logPath: filepath.Join(t.TempDir(), "daemon2.log"),
	}
	t.Cleanup(func() { d2.stop() })
	d2.start()

	// Crash-orphan reaping (v2-converge §6 bug 3): the restart
	// reconciliation reaps the crashed daemon's surviving step process
	// (journal pid + PF_WI_ID sweep) BEFORE re-dispatching, so the old
	// writer is gone while the new derivation runs.
	waitUntil(t, 30*time.Second, "the crashed daemon's step process survived the restart", func() bool {
		for _, p := range orphanPIDs(wi) {
			if p == survivorPID {
				return false
			}
		}
		return true
	})

	// The reconciliation resumed the job: a live step process again, and
	// the restarted driver seeds PAST the crashed dispatch's ordinal (the
	// fence: the journal's max is the seed, so fix re-dispatches as a
	// RESUME under ordinal 3 — never two writers on one drop path).
	waitSpawnCount(t, d2, wi, 3, 60*time.Second)
	spawns := spawnJournal(t, capDir)
	if spawns[2]["resume"] != true {
		t.Fatalf("the restart recovery was not a resume: %+v", spawns[2])
	}
	if pid3, _ := spawns[2]["pid"].(float64); int(pid3) == survivorPID {
		t.Fatalf("restart re-used the crashed pid %v", spawns[2]["pid"])
	}
	// T4 (B2): the re-derivation advanced the counter but kept the SAME
	// session — daemon restart reconciliation attaches to the session
	// that survived the crash at aihub.
	if sessAfter := inst.sessionOf(t, wi); sessAfter.Fingerprint != sessBefore.Fingerprint {
		t.Fatalf("daemon-restart recovery changed the session: %q -> %q",
			sessBefore.Fingerprint, sessAfter.Fingerprint)
	}

	// The resumed driver finished the work item inside the SAME session,
	// and the terminal wiring wrapped it.
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	if sess := inst.sessionOf(t, wi); sess.Fingerprint != "" {
		t.Fatalf("wrapped work item still names session %q", sess.Fingerprint)
	}

	// The driver faces: the whole chain done with the resume envelope in
	// place, and the reattached monitor watched the new derivation to its
	// end (job_started on the restarted daemon's watch).
	x := readExecution(t, capDir)
	for _, step := range []string{"grill", "fix", "verification"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	env3 := archivedEnvelope(t, capDir, "fix", 3)
	if env3["outcome"] != "completed" {
		t.Fatalf("fix's resume envelope wrong: %v", env3["outcome"])
	}
	kinds := d2.eventKinds(t, wi)
	if !contains(kinds, "job_started") || !contains(kinds, "job_stopped") {
		t.Fatalf("restarted daemon's events miss the lifecycle: %v", kinds)
	}
	// The crash survivor was reaped at restart; nothing leaks.
	waitUntil(t, 15*time.Second, "step processes leaked after the work item wrapped", func() bool {
		return len(orphanPIDs(wi)) == 0
	})
}
