package test

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// v1_s03: operator pause → unpause under the v1.5 session model,
// driver/envelope edition — the parked envelope agent is the live
// proof (a driver parked on an open needs_input question is designed
// stillness, and the pause/resume machinery around it is the thing
// under test).
//
//   - pf pause = the daemon close path carrying session-end(paused,
//     clean_pause=true): the hub keeps the session row (M3 — paused is
//     not terminal) and marks the operator-pause flag. Locally the close
//     terminates the driver (and its live step process with it), commits
//     and pushes the dirty capsule state, then RETAINS the job record in
//     the runtime's paused state. There is NO read-only seal and NO fresh
//     claim: the SAME session keeps owning the paused work item.
//   - pf resume = the whitelisted WI PATCH clean_pause=false, then the
//     CLI tells the holder daemon (the resume-paused route,
//     wi_seWTgHeKtaa8 / v2-converge §7): a LIVE holder re-attaches the
//     kept session and continues IN PLACE (the scenario asserts exactly
//     that below); a holder that was down attaches on its next start.
//   - the scenario drives both faces: after the resume flag lands on a
//     live holder, the work item continues in place (no restart needed);
//     then a second pause + holder-down resume exercises the restart
//     reconciliation fallback — the holder daemon restarts (graceful
//     stop + fresh start on the same data dir/workspace), the attach
//     fires, the resumed driver parks on the SAME persisted question,
//     and the owner's answer completes the workflow — the terminal
//     wiring wraps it.
func TestV1S03PauseResume(t *testing.T) {
	inst := newV1Install(t)
	const qid = "q_v1s03"
	wi := inst.newWI(t, "s03: pause then resume (retained record)",
		[]map[string]any{
			// The grill envelope parks the driver on an open question: the
			// paused-envelope-agent shape the pause close interrupts.
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "s03: park here until the operator is ready."},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	waitQuestion(t, d, wi, qid, 30*time.Second)
	sessBefore := inst.sessionOf(t, wi)
	if sessBefore.Fingerprint == "" {
		t.Fatal("running work item carries no session")
	}

	d.pf(t, "pause", wi)
	inst.waitWIStatus(t, wi, "paused", 30*time.Second)

	// The pause is the operator shape: clean_pause rides the same
	// atomic fenced session-end, and the session row is KEPT (M3).
	sess := inst.sessionOf(t, wi)
	if sess.Fingerprint != sessBefore.Fingerprint {
		t.Fatalf("pause changed the session: %q -> %q", sessBefore.Fingerprint, sess.Fingerprint)
	}
	if !sess.CleanPause {
		t.Fatalf("operator pause did not set clean_pause: %+v", sess)
	}

	// Finding 2: the record is RETAINED locally. The daemon's status
	// face shows the paused job, still carrying the SAME public
	// session fingerprint (the secret never left state.json). A driver
	// job carries no live agent pid by construction (the handle is the
	// daemon-side driver goroutine) — the retained record is agentless.
	st := d.status(t)
	if len(st.Jobs) != 1 || st.Jobs[0].WorkItemID != wi || st.Jobs[0].Status != "paused" {
		t.Fatalf("the paused job must be retained in the paused state: %+v", st.Jobs)
	}
	if st.Jobs[0].SessionFingerprint != sessBefore.Fingerprint {
		t.Fatalf("the retained record lost the session fingerprint: %+v", st.Jobs)
	}
	if pid := st.Jobs[0].AgentPID; pid != 0 {
		t.Fatalf("the pause close must record no live agent, got pid %d", pid)
	}
	// The retained scene is NOT sealed read-only (no read-only seal on a
	// pause — the later attach must be able to keep working in it).
	cst, err := statPerm(filepath.Join(d.capsuleDir(wi), "capsule.json"))
	if err != nil {
		t.Fatalf("retained capsule.json: %v", err)
	}
	if cst == 0o444 {
		t.Fatal("the paused scene must not be sealed read-only")
	}
	pausedCommits := inst.bareCommitCount(t, wi)
	if pausedCommits == 0 {
		t.Fatal("paused chain never reached the server")
	}
	// The pause committed the parked workflow state: the pending question
	// is durable in the retained capsule.
	if x := readExecution(t, d.capsuleDir(wi)); x.Steps["grill"].PendingQuestion == nil {
		t.Fatal("the parked question did not survive into the retained capsule state")
	}

	// A plain claim of the paused work item is refused typed ALREADY_HELD
	// on the wire (the session still holds it — pause is not a release;
	// M1's joint CAS gate names the holder). The CLI submit only enqueues,
	// so the typed refusal is asserted at the claim endpoint itself.
	refuseClaim(t, inst, wi, "ALREADY_HELD")

	// The operator unpause — the LIVE-HOLDER continuation
	// (wi_seWTgHeKtaa8, v2-converge §7): the PATCH clears clean_pause
	// AND the CLI tells the holder daemon, which re-attaches the kept
	// session and continues in place.
	d.pf(t, "resume", wi)
	sess = inst.sessionOf(t, wi)
	if sess.CleanPause {
		t.Fatalf("resume did not clear clean_pause: %+v", sess)
	}
	if sess.Fingerprint != sessBefore.Fingerprint {
		t.Fatalf("unpause changed the session: %+v", sess)
	}
	// The online holder picked the resume up: the work item is RUNNING
	// again under the SAME session — no daemon restart involved — and the
	// resumed driver re-poses the SAME persisted question on this
	// daemon.
	inst.waitWIStatus(t, wi, "running", 30*time.Second)
	if sess = inst.sessionOf(t, wi); sess.Fingerprint != sessBefore.Fingerprint {
		t.Fatalf("the in-place attach changed the session: %q, want %q", sess.Fingerprint, sessBefore.Fingerprint)
	}
	waitQuestion(t, d, wi, qid, 30*time.Second)
	// No dispatch while the question is open (acceptance 1): the resumed
	// driver re-poses the SAME persisted question and waits for the
	// human answer — the spawn journal still carries exactly the one
	// pre-pause derivation.
	if n := len(spawnJournal(t, d.capsuleDir(wi))); n != 1 {
		t.Fatalf("the in-place resume must re-pose the question without dispatching; spawn journal = %d entries", n)
	}

	// The holder-DOWN fallback: a second operator pause re-parks the
	// work item, the holder daemon stops, and the resume lands with no
	// daemon reachable (the flag clear alone — the CLI's guidance path).
	// The fresh daemon's restart reconciliation then attaches: the same
	// continuation, one daemon start later.
	d.pf(t, "pause", wi)
	inst.waitWIStatus(t, wi, "paused", 30*time.Second)
	d.stop()
	d.pf(t, "resume", wi)
	if sess = inst.sessionOf(t, wi); sess.CleanPause {
		t.Fatalf("holder-down resume did not clear clean_pause: %+v", sess)
	}
	d2 := &v1Daemon{
		t: t, inst: inst, sock: d.sock, dataDir: d.dataDir, workspace: d.workspace,
		machineID: d.machineID, extraArgs: d.extraArgs,
		logPath: filepath.Join(t.TempDir(), "daemon2.log"),
	}
	t.Cleanup(func() { d2.stop() })
	d2.start()

	inst.waitWIStatus(t, wi, "running", 30*time.Second)
	// The attach kept the session: same public fingerprint as before the
	// whole pause/resume cycle (a force-end + re-claim would have swapped it).
	sess = inst.sessionOf(t, wi)
	if sess.Fingerprint != sessBefore.Fingerprint {
		t.Fatalf("the attach changed the session: %q, want %q", sess.Fingerprint, sessBefore.Fingerprint)
	}
	// The resumed driver re-presents the SAME persisted question (the
	// pending question is durable; the awaiting face is total).
	waitQuestion(t, d2, wi, qid, 30*time.Second)

	// The owner's answer completes the parked step (the in-place
	// re-dispatch continues the SAME session), the workflow runs to its
	// terminal state and the terminal wiring wraps it.
	answerQuestion(t, d2, wi, qid, "Ready now: take the lean variant.")
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	sess = inst.sessionOf(t, wi)
	if sess.Fingerprint != "" {
		t.Fatalf("wrapped work item still names session %q", sess.Fingerprint)
	}

	// The chain continued ABOVE the paused tip (the hook admits only
	// fast-forward pushes to main).
	if n := inst.bareCommitCount(t, wi); n <= pausedCommits {
		t.Fatalf("chain did not grow past the pause (%d <= %d)", n, pausedCommits)
	}

	// The continuation's faces: grill done with its archived answer, the
	// whole autopilot chain done, the spawn journal carrying the pause
	// interruption (derivation 1 pre-pause, the answer's resume
	// dispatch of the re-posed question after the whole cycle, then the
	// fresh dispatches of fix and verification — neither the in-place
	// resume nor the restart attach dispatched while the question was
	// open).
	x := readExecution(t, d2.capsuleDir(wi))
	for _, step := range []string{"grill", "fix", "verification"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	if len(x.Steps["grill"].Answers) != 1 {
		t.Fatalf("grill's archived answers: %+v", x.Steps["grill"].Answers)
	}
	spawns := spawnJournal(t, d2.capsuleDir(wi))
	// The whole post-resume workflow rode the SAME session: the answer's
	// completion dispatch of the parked grill step (a resume, ordinal 2)
	// plus the fresh dispatches of fix and verification — neither the
	// in-place resume nor the restart attach dispatched anything while
	// the question was open.
	if len(spawns) != 4 || spawns[1]["resume"] != true || spawns[2]["resume"] != false {
		t.Fatalf("spawn journal after the cycle: %d entries (want 4, second a resume then fresh dispatches): %v", len(spawns), spawns)
	}

	// And no orphan of the paused close survived the whole cycle.
	waitUntil(t, 10*time.Second, "orphan step processes survived the pause/resume cycle", func() bool {
		return len(orphanPIDs(wi)) == 0
	})
}

// statPerm is os.Stat returning just the permission bits (0 on error).
func statPerm(path string) (os.FileMode, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Mode().Perm(), nil
}
