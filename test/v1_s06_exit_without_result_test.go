package test

import (
	"testing"
	"time"
)

// v1_s06_exit_without_result_test.go: the step process that dies silently without delivering,
// driver/envelope edition (v2-converge 2.3: an exit WITHOUT a result is
// the driver's own to classify — it is the one failure the driver sees
// directly, without any supervisor verdict). The grill dispatch burns an
// injected delay and then dies WITHOUT writing its result envelope. The
// driver classifies the exit (no model-class sign: same-model retry),
// records ONE failure (cause exit_other, counter 1) in execution.json,
// and re-dispatches the step in place — resuming the transcript, the
// first retry's shape — under the next ordinal; the workflow completes
// and wraps. No recovery fires for the exit (the recovery engine is the
// DRIVER's crash bound now): no needs_recovery for the whole episode —
// the completion wrap is a finish, not a death. And the supervisor stays quiet
// through the silent-but-live delay: no verdict, no driver stall.
//
// This scenario runs against the REAL `pf daemon` process.
func TestV1S06HangRecovery(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "s06: step process dies without delivering (the delayed envelope never lands)",
		[]map[string]any{
			// Ordinal 1: 4s of silence, then die before the envelope write.
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true,
				"ms": 4000, "crash": "before"},
			// Ordinal 2: the driver's in-place retry (resume).
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	capDir := d.capsuleDir(wi)
	waitUntil(t, 30*time.Second, "the before-write crash was never journaled", func() bool {
		for _, rec := range fakeJournal(t, capDir) {
			if rec["type"] == "crash_before" && rec["step"] == "grill" {
				return true
			}
		}
		return false
	})

	// The driver's own handling: ONE failure record with its cause
	// sub-class, same-model retry, resume shape.
	rec := waitForEvent(t, d, wi, "step_failed", 60*time.Second)
	var body struct {
		Step        string `json:"step"`
		Cause       string `json:"cause"`
		Disposition string `json:"disposition"`
		Count       int    `json:"failure_count"`
		Next        string `json:"next_dispatch"`
	}
	eventBody(t, rec, &body)
	if body.Step != "grill" || body.Cause != "exit_other" || body.Disposition != "retry_same_model" ||
		body.Count != 1 || body.Next != "resume" {
		t.Fatalf("step_failed = %+v, want grill/exit_other/retry_same_model/1/resume", body)
	}

	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	spawns := waitSpawnCount(t, d, wi, 4, 60*time.Second)
	if len(spawns) != 4 || spawns[1]["resume"] != true {
		t.Fatalf("the retry was not an in-place resume: %+v", spawns)
	}
	x := readExecution(t, capDir)
	grill := x.Steps["grill"]
	var failures int
	for _, a := range grill.Attempts {
		if a["cause"] == "infra_failure" {
			failures++
			if a["failure_cause"] != "exit_other" {
				t.Fatalf("attempt cause sub-class %v, want exit_other", a["failure_cause"])
			}
		}
	}
	if failures != 1 {
		t.Fatalf("grill carries %d infra_failure attempts, want exactly 1: %+v", failures, grill.Attempts)
	}

	// The whole episode fired exactly ONE recovery signal: the completion
	// wrap. The exit never reached the recovery engine, and the
	// supervisor never judged the silent-but-live delay.
	kinds := d.eventKinds(t, wi)
	if n := countOf(kinds, "needs_recovery"); n != 0 {
		t.Fatalf("needs_recovery fired %d times, want 0 (the completion is no death): %v", n, kinds)
	}
	if n := countOf(kinds, "step_dead"); n != 0 {
		t.Fatalf("the supervisor judged a live step process dead: %v", kinds)
	}

	waitUntil(t, 15*time.Second, "step processes survived the episode", func() bool {
		return len(orphanPIDs(wi)) == 0
	})
}
