package test

import (
	"fmt"
	"testing"
	"time"
)

// v1_s17: an aihub outage during a run, end to end with the REAL daemon
// riding PRODUCTION defaults, driver/envelope edition. The version
// stream is the DRIVER's own write face: each owner answer is a
// transition (the D9 archive-and-clear write plus the re-dispatch's
// step_start), so the parked question/answer cycle keeps execution.json's
// Version climbing through the outage window — every new Version is
// pushed at once. The outage is a REAL stopped aihub process; the push
// path is the real git-over-HTTP lane. Every failed push writes
// push_failed while the driver keeps working — no recovery/respawn
// signal fires. After the aihub returns, the chain catches up (the
// versions written during the outage reach the server-side bare repo)
// and a fresh published event lands. (The backoff cap's degradation
// event is pinned in v1_s18 with a shrunk ladder.)
func TestV1S17PushOutageKeepsDriverAndCatchesUp(t *testing.T) {
	inst := newV1Install(t)
	// The plan: a parked question/answer ladder (the version stream),
	// then the completion tail. Each answer consumes exactly one ordinal:
	// answer k re-dispatches grill under ordinal k+1, whose envelope parks
	// on question k+1.
	//
	// outageRounds: the outage needs >= 5 push_failed below. Measured, the
	// first outage round (plus its push_failed wait) yields ~4 and every
	// further round 2-3 more, so 5 is reached by round 2; 4 rounds land
	// ~10-11 (2x the 5 the assertions need) without leaning on the
	// time-driven retries.
	const outageRounds = 4
	actions := []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": "q_s17bp_0",
			"question": "s17: the version-stream ladder parks here."},
	}
	// outageRounds outage questions (q_1..q_outageRounds) plus one spare
	// catch-up question (q_outageRounds+1) answered after the aihub is
	// back, before the completion tail.
	for i := 1; i <= outageRounds+1; i++ {
		actions = append(actions, map[string]any{
			"do": "envelope", "id": "grill", "outcome": "needs_input",
			"question_id": fmt.Sprintf("q_s17bp_%d", i),
			"question":    fmt.Sprintf("s17: outage round %d.", i),
		})
	}
	actions = append(actions,
		map[string]any{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		map[string]any{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		map[string]any{"do": "envelope", "id": "verification", "outcome": "completed"},
	)
	wi := inst.newWI(t, "s17: push failures during an aihub outage", actions, nil)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	// round is the index of the CURRENTLY OPEN question of the ladder.
	round := 0
	advance := func(bound time.Duration) {
		answerQuestion(t, d, wi, fmt.Sprintf("q_s17bp_%d", round), fmt.Sprintf("round %d", round))
		round++
		waitQuestion(t, d, wi, fmt.Sprintf("q_s17bp_%d", round), bound)
	}
	waitQuestion(t, d, wi, fmt.Sprintf("q_s17bp_%d", round), 30*time.Second) // q_s17bp_0 parked

	// The push path is proven healthy before the outage: at least one
	// version reached the aihub's bare repo.
	waitForEvent(t, d, wi, "published", 30*time.Second)

	// The outage: stop the REAL aihub mid-run. Every boundary's push
	// fails (one push_failed per attempt).
	inst.stopAihub()
	// The outage's first boundary is the next answer's transition (the
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

	// The final answer completes the workflow; the terminal wiring seals
	// it with the aihub back up.
	answerQuestion(t, d, wi, fmt.Sprintf("q_s17bp_%d", round), "the last answer")
	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	x := readExecution(t, d.capsuleDir(wi))
	for _, step := range []string{"grill", "fix", "verification"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	// Still exactly the one attempt instance across the whole episode.
	if n := countOf(d.eventKinds(t, wi), "needs_recovery"); n != 1 {
		t.Fatalf("needs_recovery fired %d times, want 1 (the completion signal): %v", n, d.eventKinds(t, wi))
	}
}
