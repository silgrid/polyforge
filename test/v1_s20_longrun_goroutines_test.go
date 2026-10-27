package test

// v1_s20: the long-run leak probe — scenario B of the dual-scenario
// split the owner confirmed (grill q_f17c2e): the GOROUTINE half.
// After 20 strictly consecutive jobs run through the in-process rig
// (the SAME production components the real daemon assembles —
// runtime.New, observe.New, DefaultSpawner, the real observe UDS
// routes), the process's goroutine count must return to within a small
// fixed slack of the baseline captured after rig start-up idle-settle
// and BEFORE job 1. A per-job goroutine leak (a monitor, an agent
// stdout reader or an exit watcher that never retires) would instead accumulate one or more goroutines per job and
// miss the bar by 20x that.
//
// Division of labor (the dual-scenario split): goroutines are IN-PROCESS
// semantics — the real `pf daemon` is a separate process whose
// runtime.NumGoroutine no test can read, and the owner explicitly
// rejected adding test-only introspection hooks to the shipped binary
// (option B). So the fd and events halves of the probe live in the
// companion scenario v1_s19_longrun_leak_test.go against the REAL
// daemon binary (process-level reality), and this file carries the
// goroutine assertion through the rig, the faithful already-existing
// carrier. Each job here must reach its wrapped/terminal state and its
// monitor must retire before the next one starts.

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// Probe constants. The settle windows match the companion s19 scenario:
// 2s of start-up idle-settle lets the rig's own standing loops
// (runtime Run, observe Run, the UDS http server) reach
// steady state before the baseline; 3s of post-run settle lets the last
// job's async teardown finish before the final measurement. The
// goroutine sample takes the MINIMUM of 10 reads 100ms apart so
// transient scheduling/GC noise can only make a measurement MORE
// conservative, never mask a leak: a real leaked goroutine exists in
// every read, so the minimum still carries it. The slack is the small
// fixed tolerance the memo pins: background runtime goroutines (GC
// workers, http server bookkeeping) fluctuate by a couple, exact
// equality is not the bar — but 20 jobs leaking even ONE goroutine
// each would land at baseline+20, four times past the slack.
const (
	s20Jobs          = 20
	s20StartupSettle = 2 * time.Second
	s20FinalSettle   = 3 * time.Second
	s20GCSlack       = 5
	s20Samples       = 10
	s20SampleGap     = 100 * time.Millisecond
)

// s20MinGoroutines samples runtime.NumGoroutine n times, gap apart,
// and returns the minimum (see the constants block for why the
// minimum).
func s20MinGoroutines(n int, gap time.Duration) int {
	min := -1
	for i := 0; i < n; i++ {
		c := runtime.NumGoroutine()
		if min < 0 || c < min {
			min = c
		}
		if i < n-1 {
			time.Sleep(gap)
		}
	}
	return min
}

func TestV1S20LongRunGoroutines(t *testing.T) {
	inst := newV1Install(t)
	rig := newInprocRig(t, inst)

	// Baseline: after start-up idle-settle, before job 1. The aihub
	// HTTP pool's idle connections park two goroutines per connection in
	// THIS process (the rig's runtime and the test's status polls share
	// inst.client); CloseIdleConnections gives both sides of the
	// comparison the same deterministic HTTP-pool floor of zero.
	time.Sleep(s20StartupSettle)
	inst.client.HTTP.CloseIdleConnections()
	baseline := s20MinGoroutines(s20Samples, s20SampleGap)
	t.Logf("s20: goroutine baseline after start-up settle: %d", baseline)

	// 20 strictly consecutive jobs: each is a driver-driven autopilot run
	// (one envelope action per step; the last step completes the
	// workflow and the terminal wiring wraps it), and the next one starts
	// only after this one reached its terminal state AND its per-job
	// machinery — runtime job entry, observe monitor,
	// engine handle and step processes — fully retired.
	for i := 0; i < s20Jobs; i++ {
		wi := inst.newWI(t, fmt.Sprintf("s20: long-run goroutine probe job %02d", i+1),
			envelopeAutopilot(), nil)
		t.Cleanup(func() { killOrphans(wi) })
		rig.start(wi)

		// Terminal state first: the agent's flush(wrap) sealed and
		// session-end marked the work item wrapped at aihub.
		inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)

		// The runtime dropped the job from its live set.
		waitUntil(t, 30*time.Second, fmt.Sprintf("runtime never dropped job %s", wi), func() bool {
			for _, ji := range rig.rt.List() {
				if ji.WorkItem == wi {
					return false
				}
			}
			return true
		})
		// The per-WI observe monitor retired.
		waitUntil(t, 30*time.Second, fmt.Sprintf("observe monitor of %s never retired", wi), func() bool {
			_, monitored := rig.obs.Progress(wi)
			return !monitored
		})
		// No agent process survives carrying the work item's identity.
		waitUntil(t, 10*time.Second, fmt.Sprintf("agent processes of %s survived the wrap", wi), func() bool {
			return len(orphanPIDs(wi)) == 0
		})
	}

	// Final measurement: post-run settle, the same deterministic
	// HTTP-pool floor, then the same min-of-samples read. The bar is
	// "falls back to within a small fixed slack of baseline".
	time.Sleep(s20FinalSettle)
	inst.client.HTTP.CloseIdleConnections()
	final := s20MinGoroutines(s20Samples, s20SampleGap)
	if final > baseline+s20GCSlack {
		t.Fatalf("goroutine count did not fall back to baseline after %d consecutive jobs: baseline %d, final %d (slack %d)",
			s20Jobs, baseline, final, s20GCSlack)
	}
	t.Logf("s20: goroutine final after settle: %d (baseline %d, slack %d)", final, baseline, s20GCSlack)
}
