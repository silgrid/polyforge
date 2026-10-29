package observe

// retry_wait_stall_test.go: the driver's declared retry backoff
// (step_retry_wait) is not a stalled driver. Between a failed dispatch's
// step_ended and its retry the driver may wait up to 9 minutes by
// decision; the supervisor's between-steps clock N starts at the declared
// end of that wait, so neither the default N (10m) nor a shorter
// configured one relaunches a driver that is merely pacing its retry.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// stalled reports whether the monitor raised driver_stalled.
func stalled(m *Monitor) bool {
	for _, v := range m.link.Verdicts.Drain() {
		if _, ok := v.(supervise.DriverStalled); ok {
			return true
		}
	}
	return false
}

// tickFor drives one tick per minute for d (inside the observation-gap
// threshold, so no tick declares a pause).
func tickFor(m *Monitor, clk *pauseClock, d time.Duration) {
	for end := clk.Now().Add(d); clk.Now().Before(end); {
		clk.Advance(time.Minute)
		tick(m)
	}
}

// endDispatch runs one dispatch to its step_ended at the clock's instant.
func endDispatch(m *Monitor, clk *pauseClock) {
	dispatch(m, clk, supervise.Thresholds{})
	tick(m)
	m.link.Fact(supervise.StepEnded{Step: "fix", SpawnN: 1, Outcome: supervise.OutcomeExitedWithoutResult})
	tick(m)
}

func TestDeclaredRetryWaitDefersTheStallClock(t *testing.T) {
	for _, stall := range []time.Duration{10 * time.Minute, 5 * time.Minute} {
		clk := newPauseClock(time.Unix(1_700_000_000, 0))
		m := newTickDrivenMonitor(t, clk, time.Minute)
		m.cfg.DriverStall = stall
		endDispatch(m, clk)
		wait := 9 * time.Minute
		m.link.Fact(supervise.StepRetryWait{Step: "fix", Cause: "model_error", Until: clk.Now().Add(wait)})
		tick(m)
		tickFor(m, clk, wait+stall-time.Minute)
		if stalled(m) {
			t.Fatalf("stall %v: driver_stalled raised inside the declared %v wait plus N", stall, wait)
		}
		tickFor(m, clk, 2*time.Minute)
		if !stalled(m) {
			t.Fatalf("stall %v: a driver still between steps N after the declared wait must be judged stalled", stall)
		}
	}
}

// The control: without the declaration the same gap is a stall at N.
func TestUndeclaredGapStillStallsAtN(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	m.cfg.DriverStall = 5 * time.Minute
	endDispatch(m, clk)
	tickFor(m, clk, 6*time.Minute)
	if !stalled(m) {
		t.Fatalf("an undeclared 6m gap between steps must be judged stalled at N=5m")
	}
}

// A declaration that ends before the clock already starts changes
// nothing (a stale or zero-length wait never rewinds N).
func TestPastRetryWaitDoesNotRewindTheStallClock(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	m.cfg.DriverStall = 5 * time.Minute
	endDispatch(m, clk)
	since := m.phaseSince
	m.link.Fact(supervise.StepRetryWait{Step: "fix", Cause: "model_error", Until: since.Add(-time.Minute)})
	tick(m)
	if !m.phaseSince.Equal(since) {
		t.Fatalf("a past declaration moved the stall clock from %v to %v", since, m.phaseSince)
	}
}

// A host freeze that spans the declared wait's end: the stall clock
// would start at Until inside the unobserved span, and the first tick
// after the freeze would read the frozen remainder as driver time. The
// observation-gap discount starts N at the resume instead.
func TestFreezeSpanningRetryWaitEndDoesNotStall(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	m.cfg.DriverStall = 10 * time.Minute
	endDispatch(m, clk)
	m.link.Fact(supervise.StepRetryWait{Step: "fix", Cause: "model_error", Until: clk.Now().Add(9 * time.Minute)})
	tick(m)
	tickFor(m, clk, time.Minute)  // T0+1m: observed
	clk.Advance(20 * time.Minute) // frozen T0+1m .. T0+21m, across Until (T0+9m)
	tick(m)
	if stalled(m) {
		t.Fatalf("the first tick after a freeze spanning the wait's end raised driver_stalled")
	}
	tickFor(m, clk, 9*time.Minute)
	if stalled(m) {
		t.Fatalf("driver_stalled raised inside N of the resume")
	}
	tickFor(m, clk, 2*time.Minute)
	if !stalled(m) {
		t.Fatalf("a driver still between steps N after the resume must be judged stalled")
	}
}
