package test

import (
	"fmt"
	"testing"
	"time"
)

// v1_s17: an aihub outage during a run, end to end with the REAL daemon
// riding PRODUCTION defaults (but a short ask warm window), driver/envelope
// edition. The version stream is the DRIVER's own write face: each owner
// reply to an ask whose warm window already ended the process resumes the
// session under a new dispatch (its step_start transition), so the
// ask/reply cycle keeps execution.json's Version climbing through the
// outage window — every new Version is pushed at once. The outage is a REAL stopped aihub process; the push
// path is the real git-over-HTTP lane. Every failed push writes
// push_failed while the driver keeps working — no recovery/respawn
// signal fires. After the aihub returns, the chain catches up (the
// versions written during the outage reach the server-side bare repo)
// and a fresh published event lands. (The backoff cap's degradation
// event is pinned in v1_s18 with a shrunk ladder.)
func TestV1S17PushOutageKeepsDriverAndCatchesUp(t *testing.T) {
	inst := newV1Install(t)
	// The plan: an ask/reply ladder (the version stream), then the
	// completion tail. Each reply consumes exactly one ordinal: reply k
	// resumes fix under ordinal k+1, which asks ask k+1.
	//
	// outageRounds: the outage needs >= 5 push_failed below. A reply makes
	// one Version (the resume dispatch's step_start), so one push. Measured
	// (isolated, twice): the first outage round, with its push_failed wait,
	// yields 2 and every further round exactly 1 more — n rounds land
	// n+1, so 4 rounds would land exactly the 5 needed (a loaded run saw
	// 4). 9 rounds land ~10, 2x the 5 the assertions need, without
	// leaning on the time-driven retries.
	const outageRounds = 9
	actions := []map[string]any{askAction("fix", "s17: the version-stream ladder parks here.", nil)}
	// outageRounds outage asks plus one spare catch-up ask replied to after
	// the aihub is back, before the completion tail.
	for i := 1; i <= outageRounds+1; i++ {
		actions = append(actions, askAction("fix", fmt.Sprintf("s17: outage round %d.", i), nil))
	}
	actions = append(actions,
		map[string]any{"do": "envelope", "id": "fix", "outcome": "completed"},
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed"},
	)
	wi := inst.newWI(t, "s17: push failures during an aihub outage", actions, nil)
	d := newV1Daemon(t, inst)
	d.extraArgs = append(d.extraArgs, "--ask-warm-window", "2s")
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	// round is the index of the CURRENTLY OPEN ask of the ladder; open its
	// stamp. A reply waits for the warm window to end the asking process,
	// so it resumes the session under the next dispatch (a transition).
	round := 0
	open := ""
	nextAsk := func(bound time.Duration) {
		prev := open
		waitUntil(t, bound, fmt.Sprintf("ask %d never opened", round), func() bool {
			open = questionIDOf(statusQuiet(d), wi)
			return open != "" && open != prev
		})
	}
	reply := func(text string) {
		waitUntil(t, 30*time.Second, "the warm window never ended the asking process", func() bool { return len(orphanPIDs(wi)) == 0 })
		replyOwner(t, d, wi, text)
	}
	advance := func(bound time.Duration) {
		reply(fmt.Sprintf("round %d", round))
		round++
		nextAsk(bound)
	}
	nextAsk(30 * time.Second) // ask 0 parked

	// The push path is proven healthy before the outage: at least one
	// version reached the aihub's bare repo.
	waitForEvent(t, d, wi, "published", 30*time.Second)

	// The outage: stop the REAL aihub mid-run. Every boundary's push
	// fails (one push_failed per attempt).
	inst.stopAihub()
	// The outage's first boundary is the next reply's resume dispatch (the
	// parked version's wave already landed).
	advance(30 * time.Second)
	waitForEvent(t, d, wi, "push_failed", 60*time.Second)
	// Every outage round keeps the driver writing versions, each pushed
	// at once against the dead aihub.
	for round < outageRounds {
		advance(30 * time.Second)
	}
	waitUntil(t, 60*time.Second, "five push_failed events during the outage", func() bool {
		return countOf(d.eventKinds(t, wi), "push_failed") >= 5
	})

	// Push failures never kill the driver: no recovery/respawn signal, no
	// new attempt instance, and the work item is still running.
	kinds := d.eventKinds(t, wi)
	for _, bad := range []string{"wi_agent_rederived", "needs_recovery", "job_stopped"} {
		if n := countOf(kinds, bad); n != 0 {
			t.Fatalf("%s fired %d times during the outage: %v", bad, n, kinds)
		}
	}
	if n := countOf(kinds, "job_started"); n != 1 {
		t.Fatalf("job_started fired %d times, want 1 (no new attempt instance): %v", n, kinds)
	}
	if n := countOf(kinds, "push_failed"); n < 5 {
		t.Fatalf("push_failed fired %d times during the outage, want >= 5: %v", n, kinds)
	}

	// The catch-up baseline: what the driver wrote during the outage lives
	// as local commits in the capsule's chain. Bring the aihub back,
	// produce one more boundary, and demand the server-side bare repo
	// reach that history plus a fresh published event — the failed pushes
	// were never lost work.
	local := capsuleCommitCount(t, d.dataDir, wi)
	if local == 0 {
		t.Fatal("no local commits after the outage episode")
	}
	publishedBefore := countOf(kinds, "published")
	inst.restartAihub()
	advance(30 * time.Second) // the catch-up boundary
	inst.waitBareCommits(t, wi, local, 60*time.Second)
	waitUntil(t, 60*time.Second, "no published event after the recovery push", func() bool {
		return countOf(d.eventKinds(t, wi), "published") > publishedBefore
	})

	// The final reply completes the workflow; the terminal wiring seals
	// it with the aihub back up.
	reply("the last answer")
	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	x := readExecution(t, d.capsuleDir(wi))
	for _, step := range []string{"fix", "review"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	// Still exactly the one attempt instance across the whole episode (and
	// the completion records no needs_recovery).
	if n := countOf(d.eventKinds(t, wi), "needs_recovery"); n != 0 {
		t.Fatalf("needs_recovery fired %d times, want 0 (the completion is no death): %v", n, d.eventKinds(t, wi))
	}
}
