package test

import (
	"strings"
	"testing"
	"time"
)

// v1_s07: repeated gate failures are bounded, driver/envelope edition.
// The legacy storm watched the record stream for consecutive provider
// errors; the driver-era equivalent storm window is the workflow's OWN
// gate-fail ladder: the verification gate FAILs with a populated error
// verdict three times, the on_fail rewind-to-fix policy fires twice (the
// autopilot bundle's rewind bounds: max_total 2, max_per_step 2), and the
// third FAIL hits the bound — failed_rewind_exhausted. The daemon's v2
// terminal wiring translates the driver's exit into the failed close:
// the recovery cause is the failure signal, not a death. Every archived
// attempt, verdict and rewind is on the execution.json / envelope faces.
func TestV1S07ProviderErrorStorm(t *testing.T) {
	inst := newV1Install(t)
	verdict := map[string]any{
		"error":   "upstream connection reset after 2 retries (fake harness injected)",
		"verdict": "FAIL",
	}
	wi := inst.newWI(t, "s07: consecutive gate failures, bounded",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			// FAIL #1 → rewind [fix, verification) → fix.
			{"do": "envelope", "id": "verification", "outcome": "gate_failed", "output": verdict},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			// FAIL #2 → rewind #2 (the last the bounds allow).
			{"do": "envelope", "id": "verification", "outcome": "gate_failed", "output": verdict},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			// FAIL #3 → the bounds are spent: failed_rewind_exhausted.
			{"do": "envelope", "id": "verification", "outcome": "gate_failed", "output": verdict},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	// The bounded terminal state on the execution.json face: the workflow
	// failed_rewind_exhausted after exactly two executed rewinds, and the
	// gate itself is NEVER done — it holds the third FAIL verdict.
	waitWorkflowStatus(t, d, wi, "failed_rewind_exhausted", 120*time.Second)
	capDir := d.capsuleDir(wi)
	x := readExecution(t, capDir)
	if x.ReviewRounds["verification"] != 3 {
		t.Fatalf("review rounds %v, want 3 verdicts recorded", x.ReviewRounds)
	}
	if n := int(x.RewindStats["count"].(float64)); n != 2 {
		t.Fatalf("rewind count %v, want exactly 2 (the third hit the bound)", x.RewindStats["count"])
	}
	ver := x.Steps["verification"]
	if ver == nil || ver.Status != "in_progress" {
		t.Fatalf("the gate step must hold the verdict in_progress, got %+v", ver)
	}
	if ver.Output["error"] == nil || !strings.Contains(ver.Output["error"].(string), "connection reset") {
		t.Fatalf("the FAIL verdict output is not on the gate: %+v", ver.Output)
	}
	// The two executed rewinds archived fix's superseded outputs as
	// gate_fail_rewind attempts — the preference order's archive face.
	fixArchives := 0
	for _, a := range x.Steps["fix"].Attempts {
		if a["cause"] == "gate_fail_rewind" {
			fixArchives++
		}
	}
	if fixArchives != 2 {
		t.Fatalf("fix archived %d gate_fail_rewind attempts, want 2: %+v", fixArchives, x.Steps["fix"].Attempts)
	}
	// Every ordinal's envelope is on the archive face, in plan order —
	// including both error envelopes with their populated verdicts.
	for i, want := range []struct {
		step    string
		outcome string
	}{
		{"grill", "completed"}, {"fix", "completed"}, {"verification", "gate_failed"},
		{"fix", "completed"}, {"verification", "gate_failed"}, {"fix", "completed"},
		{"verification", "gate_failed"},
	} {
		env := archivedEnvelope(t, capDir, want.step, i+1)
		if env["outcome"] != want.outcome {
			t.Fatalf("archived envelope s%d (%s): outcome %v, want %s", i+1, want.step, env["outcome"], want.outcome)
		}
		if want.outcome == "gate_failed" && env["output"] == nil {
			t.Fatalf("error envelope s%d carries no verdict output", i+1)
		}
	}

	// The daemon's v2 terminal wiring closed the WI as FAILED (the
	// recovery cause was the failure signal), and the log says so.
	inst.waitWIStatus(t, wi, "failed", 60*time.Second)
	waitUntil(t, 30*time.Second, "the daemon log never recorded the failed close", func() bool {
		return strings.Contains(d.logTail(), "driver workflow failed (failed_rewind_exhausted)")
	})

	// ONE recovery signal for the whole ladder: the failure translation.
	// The rewind cycles never left the driver (no re-derivation, no
	// storm) — the storm bound that matters is the workflow's own.
	kinds := d.eventKinds(t, wi)
	if n := countOf(kinds, "needs_recovery"); n != 1 {
		t.Fatalf("needs_recovery fired %d times, want 1 (the failure signal): %v", n, kinds)
	}
	if n := countOf(kinds, "wi_agent_rederived"); n != 0 {
		t.Fatalf("re-derivation count %d, want 0 (the ladder ran inside one driver): %v", n, kinds)
	}
	// The failed close cleared the session (no holder on a terminal WI)
	// and the sealed chain carries the bounded ladder's archives. (The
	// terminal workflow_status itself rides the last C3 push wave, which
	// races the failed close — the local execution.json is the authority
	// for the verdict; the chain reliably carries the rewind archives.)
	if sess := inst.sessionOf(t, wi); sess.Fingerprint != "" {
		t.Fatalf("failed work item still names session %q", sess.Fingerprint)
	}
	waitUntil(t, 30*time.Second, "the sealed chain never carried the rewind archives", func() bool {
		return inst.bareHasPath(t, wi, "runtime-state/execution.json") &&
			strings.Contains(inst.bareFileAtMain(t, wi, "runtime-state/execution.json"), "gate_fail_rewind")
	})
	if n := len(spawnJournal(t, capDir)); n != 7 {
		t.Fatalf("spawn journal has %d entries, want 7 (seven step processes, zero re-derivations)", n)
	}
}
