package observe

// supervisor_pause_event_test.go: the two observable parts of the
// fix that the repro does not encode.
//
//   - acceptance 3: one durable event per observation gap naming how long
//     the supervisor was paused (the ticket explicitly leaves the kind to
//     the fix, then requires "one event ... how long");
//   - the model/tool time split: a phase that was open when the host froze
//     and closed after the resume must not carry the frozen span into the
//     split that classifies step_timeout_long_task vs _slow_model.
//
// Both are driven through the monitor's tick path against the same
// injectable clock as the repro (newTickDrivenMonitor/tick/dispatch live in
// supervisor_pause_repro_test.go, same package). Each test fails on the
// pre-fix tree at runtime: no event, and a split of a full hour.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// The event test uses the kind as a literal ON PURPOSE: it must compile
// against the pre-fix tree (where EvSupervisorPaused does not exist) so its
// failure there is "no event", not a build error.
func TestSupervisorPauseIsRecordedOncePerGap(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	dispatch(m, clk, supervise.Thresholds{
		Silence: 10 * time.Hour, Tool: 10 * time.Hour, StepTimeout: 10 * time.Hour,
	})
	tick(m) // the first observation: the window opens here
	noVerdict(t, m, "the opening tick")

	clk.Advance(time.Hour)
	tick(m) // the first tick after the gap: one pause, one event
	noVerdict(t, m, "the first tick after the gap")

	evs := eventsOf(m, "supervisor_paused")
	if len(evs) != 1 {
		t.Fatalf("supervisor_paused events = %d, want exactly one per gap", len(evs))
	}
	var body struct {
		PausedMs       int64 `json:"paused_ms"`
		PollIntervalMs int64 `json:"poll_interval_ms"`
	}
	if err := json.Unmarshal(evs[0].Body, &body); err != nil {
		t.Fatalf("supervisor_paused body %s: %v", evs[0].Body, err)
	}
	if body.PausedMs != time.Hour.Milliseconds() {
		t.Fatalf("paused_ms = %d, want %d (the full tick-to-tick gap)", body.PausedMs, time.Hour.Milliseconds())
	}
	if body.PollIntervalMs != time.Minute.Milliseconds() {
		t.Fatalf("poll_interval_ms = %d, want the declared cadence %d", body.PollIntervalMs, time.Minute.Milliseconds())
	}

	// A normal tick afterwards is not a new gap.
	clk.Advance(time.Minute)
	tick(m)
	if n := len(eventsOf(m, "supervisor_paused")); n != 1 {
		t.Fatalf("supervisor_paused events = %d after a normal tick, want 1", n)
	}
}

// A tool that was already running when the host froze and whose ToolEnd is
// folded right after the resume (before the first tick) must not book the
// frozen hour into the tool time.
func TestFrozenToolCloseDoesNotBookTheGap(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	tr := dispatch(m, clk, supervise.Thresholds{
		Silence: 10 * time.Hour, Tool: 10 * time.Hour, StepTimeout: 10 * time.Hour,
	})
	tick(m)
	tr.ToolStart("t1", "bash", `{"command":"go test ./..."}`)

	clk.Advance(time.Hour) // host frozen; the tool is frozen with it
	tr.ToolEnd("t1", "bash")
	tick(m)

	if got := tr.Snapshot(clk.Now()).ToolTime; got > 2*time.Minute {
		t.Fatalf("tool time after a 1h observation gap = %v, want ~one poll interval (the frozen hour must not count)", got)
	}
}

// A phase that was already open when the host froze and whose end is folded
// after the first post-gap tick must still book its observed span. The gap
// tick already shifted the open phase's start, so closing it later must not
// subtract the gap a second time.
func TestFrozenToolCloseAfterGapTickBooksObservedSpan(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	tr := dispatch(m, clk, supervise.Thresholds{
		Silence: 10 * time.Hour, Tool: 10 * time.Hour, StepTimeout: 10 * time.Hour,
	})
	start := clk.Now()
	tr.ToolStart("t1", "bash", `{"command":"go test ./..."}`)
	// The last pre-gap observation comes well after the phase started, and
	// the freeze follows it.
	clk.Advance(58 * time.Minute)
	tick(m)
	clk.Advance(time.Hour) // host frozen
	tick(m)                // the first tick after the gap shifts the open phase's start

	clk.Advance(30 * time.Second)
	end := clk.Now()
	tr.ToolEnd("t1", "bash") // closes after the gap tick, before the next one
	tick(m)                  // the next observation only closes the window

	want := end.Sub(start) - (time.Hour - time.Minute) // the observed span: wall span minus the declared gap
	if got := tr.Snapshot(clk.Now()).ToolTime; got != want {
		t.Fatalf("tool time after closing past a gap tick = %v, want exactly the observed span %v", got, want)
	}
}

// The between-steps clock (driver_stalled) is judged from the same wall
// span: a host freeze between steps must not look like a driver stuck for
// the frozen hour.
func TestSupervisorPauseDoesNotStallTheDriver(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	m.cfg.DriverStall = 10 * time.Minute
	tick(m) // between steps, window opens
	clk.Advance(time.Hour)
	tick(m) // the first tick after the gap
	noVerdict(t, m, "the first tick after a 1h observation gap between steps")
}

// The same for the model side: a model wait open at the freeze, closed by
// the first post-resume tool start before the tick.
func TestFrozenModelCloseDoesNotBookTheGap(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	tr := dispatch(m, clk, supervise.Thresholds{
		Silence: 10 * time.Hour, Tool: 10 * time.Hour, StepTimeout: 10 * time.Hour,
	})
	tick(m)
	tr.WaitingModel()

	clk.Advance(time.Hour)
	tr.ToolStart("t1", "bash", `{"command":"go test ./..."}`) // closes the model phase
	tick(m)

	if got := tr.Snapshot(clk.Now()).ModelTime; got > 2*time.Minute {
		t.Fatalf("model time after a 1h observation gap = %v, want ~one poll interval (the frozen hour must not count)", got)
	}
}
