package observe

// supervisor_pause_repro_test.go: the supervisor's OWN observation gap
// (the 2026-10-26 04:37 incident). The host froze twice
// between 03:30 and 04:37; the daemon froze with it. When the daemon came
// back, the first tick judged every in-flight dispatch against wall-clock
// spans it had never observed — three work items were killed within 1.5s
// (tool_hung 1h8m8s / 58m53s bound 30m, model_hung 46m54s bound 10m) —
// although their step processes had been frozen with the daemon and had
// not hung at all.
//
// The tests drive the monitor's tick path DIRECTLY (the same onTick the
// run loop calls on every ticker beat) against an injectable clock, so a
// tick far apart from the previous one is expressed exactly and without
// timing flakiness: cfg.PollInterval is the declared tick cadence, one
// tick is driven per PollInterval, and "the observer stopped" is a tick
// that is simply not driven for many PollIntervals while the clock
// advances. In production ticks are ~5ms of wall clock apart; here they
// are one clock minute apart, so the same relations hold.

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// pauseClock is the injectable wall clock (safe from any goroutine).
type pauseClock struct{ nanos atomic.Int64 }

func newPauseClock(t time.Time) *pauseClock {
	c := &pauseClock{}
	c.nanos.Store(t.UnixNano())
	return c
}

func (c *pauseClock) Now() time.Time { return time.Unix(0, c.nanos.Load()) }

// Advance moves the wall clock forward without anybody observing the move
// (a restart of the observer's world, not of its watches).
func (c *pauseClock) Advance(d time.Duration) time.Time {
	return time.Unix(0, c.nanos.Add(int64(d)))
}

// newTickDrivenMonitor builds a monitor whose ticks the TEST drives
// (m.onTick is the only execution path the run loop uses for judging, so
// no goroutine is needed and the tick spacing is exact). poll is the
// declared tick cadence.
func newTickDrivenMonitor(t *testing.T, clk *pauseClock, poll time.Duration) *Monitor {
	t.Helper()
	dir := t.TempDir()
	log, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	capsuleDir := filepath.Join(dir, "capsule")
	if err := os.MkdirAll(filepath.Join(capsuleDir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testCfg()
	cfg.PollInterval = poll
	link := supervise.NewLink("wi_test")
	link.Now = clk.Now // fixed before the first tick: no race, no real time
	m := &Monitor{
		wi:         WIInfo{WorkItem: "wi_test", CapsuleDir: capsuleDir},
		cfg:        cfg,
		log:        log,
		ob:         supervise.NewQueue[protocol.OBMsg](),
		link:       link,
		rtQ:        supervise.NewQueue[Attach](),
		stopCh:     make(chan struct{}),
		gate:       make(chan struct{}, 1),
		pushDone:   make(chan pushResult, 1),
		verifyDone: make(chan verifyResult, 1),
	}
	m.attach(Attach{
		Msg:    protocol.RTMsg{Kind: protocol.RTStarted, WorkItem: "wi_test", SpawnN: 1},
		Handle: newFakeHandle("att_1"),
	})
	m.phase = phaseBetween
	m.phaseSince = m.now()
	return m
}

// tick is one supervisor tick (what run's ticker case calls).
func tick(m *Monitor) { m.onTick() }

// noVerdict fails the test when the supervisor raised anything. The
// message carries the verdict's own evidence: a repro must show WHAT the
// supervisor claimed, not merely that it spoke.
func noVerdict(t *testing.T, m *Monitor, what string) {
	t.Helper()
	if vs := m.link.Verdicts.Drain(); len(vs) > 0 {
		for _, v := range vs {
			if dead, ok := v.(supervise.StepDead); ok {
				t.Fatalf("%s: unexpected step_dead: cause=%s boundary-crossing evidence=%q",
					what, dead.Cause, dead.Evidence)
			}
			t.Fatalf("%s: unexpected verdict: %#v", what, v)
		}
	}
}

// dispatch starts one dispatch at the clock's current instant.
func dispatch(m *Monitor, clk *pauseClock, th supervise.Thresholds) *supervise.Tracker {
	m.link.Fact(supervise.StepStarted{
		Step: "fix", SpawnN: 1, Model: "m", Harness: "pi",
		StartedAt: clk.Now(), Thresholds: th,
	})
	return m.link.Tracker(1)
}

// The incident's first family: a tool was in flight when the host froze.
// Its process was frozen too (no ToolEnd, no new signal). The first tick
// after the daemon came back must not charge the frozen wall time to it.
func TestHostFreezeDoesNotJudgeInFlightToolDead(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	tr := dispatch(m, clk, supervise.Thresholds{
		Silence: 30 * time.Minute, Tool: 30 * time.Minute, StepTimeout: 90 * time.Minute,
	})
	tick(m) // tick 1: the dispatch is adopted, nothing is stale yet
	noVerdict(t, m, "tick 1 (dispatch start)")

	tr.ToolStart("t1", "bash", `{"command":"go test ./..."}`)
	tick(m) // tick 2, one tick later: the tool has just begun
	noVerdict(t, m, "tick 2 (tool just started)")

	// 03:30-04:37 of the incident: the host freezes, and the daemon —
	// supervisor and step processes alike — with it. No tick runs for an
	// hour, and no harness signal arrives either.
	clk.Advance(time.Hour)

	tick(m) // the FIRST tick after the freeze
	noVerdict(t, m, "the first tick after a 1h observation gap (tool still in flight)")

	clk.Advance(time.Minute)
	tick(m)
	noVerdict(t, m, "a minute after the observation gap")
}

// The incident's second family: the dispatch was waiting for the model
// when the host froze. Same shape, model_hung instead of tool_hung.
func TestHostFreezeDoesNotJudgeModelWaitDead(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	tr := dispatch(m, clk, supervise.Thresholds{
		Silence: 10 * time.Minute, Tool: 30 * time.Minute, StepTimeout: 90 * time.Minute,
	})
	tick(m)
	noVerdict(t, m, "tick 1 (dispatch start)")

	tr.WaitingModel()
	tick(m)
	noVerdict(t, m, "tick 2 (model wait just began)")

	clk.Advance(47 * time.Minute) // model_hung 46m54s in the incident

	tick(m)
	noVerdict(t, m, "the first tick after a 47m observation gap (waiting for the model)")

	clk.Advance(time.Minute)
	tick(m)
	noVerdict(t, m, "a minute after the observation gap")
}

// The cumulative step bound (T) is charged the same way, and it fires even
// when the step DID produce fresh work right after the gap: the frozen
// hour must not count into the running time. This is the acceptance shape
// "normal work signals before AND after the gap".
func TestHostFreezeDoesNotCountTowardStepTimeout(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	tr := dispatch(m, clk, supervise.Thresholds{
		Silence: 10 * time.Minute, Tool: 30 * time.Minute, StepTimeout: 45 * time.Minute,
	})
	tick(m)
	noVerdict(t, m, "tick 1 (dispatch start)")

	tr.ModelOutput(supervise.OutputText) // work before the gap
	tick(m)
	noVerdict(t, m, "tick 2 (work before the gap)")

	// The freeze: an hour of wall clock with no observation at all.
	clk.Advance(time.Hour)

	// The step (frozen with the daemon) resumes: work before the
	// supervisor's next tick sees it.
	tr.ModelOutput(supervise.OutputText)

	tick(m) // the first tick after the gap: the step is provably working
	noVerdict(t, m, "the first tick after a 1h observation gap (step resumed with work)")

	clk.Advance(time.Minute)
	tr.ModelOutput(supervise.OutputText)
	tick(m)
	noVerdict(t, m, "a minute after the observation gap")
}

// CONTROL (acceptance 2): a dispatch that really stops working is still
// killed inside its bound while the supervisor ticks normally. No freeze
// is simulated here — every tick is one declared PollInterval apart, so an
// implementation that ignores time it never observed must still judge the
// silence it did observe.
func TestRealHangIsStillJudgedWithinBound(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	tr := dispatch(m, clk, supervise.Thresholds{
		Silence: 90 * time.Minute, Tool: 30 * time.Minute, StepTimeout: 5 * time.Hour,
	})
	tick(m)
	tr.ToolStart("t1", "bash", `{"command":"go test ./..."}`)

	var dead *supervise.StepDead
	for i := 0; i < 31; i++ { // one observed minute per tick
		clk.Advance(time.Minute)
		tick(m)
		for _, v := range m.link.Verdicts.Drain() {
			if d, ok := v.(supervise.StepDead); ok {
				dead = &d
			}
		}
		if dead != nil {
			break
		}
	}
	if dead == nil || dead.Cause != supervise.CauseToolHung {
		t.Fatalf("a real 30m tool hang must still be judged tool_hung; got %+v", dead)
	}
}

// CONTROL (acceptance 2, after a freeze): the rules re-arm across an
// observation gap instead of staying disarmed. The tool that was in flight
// when the host froze gets judged again once the supervisor has OBSERVED a
// full bound of silence — and only then.
func TestRealHangAfterADeclaredGapIsJudgedAgain(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	tr := dispatch(m, clk, supervise.Thresholds{
		Silence: 90 * time.Minute, Tool: 30 * time.Minute, StepTimeout: 5 * time.Hour,
	})
	tick(m)
	tr.ToolStart("t1", "bash", `{"command":"go test ./..."}`)
	tick(m)

	clk.Advance(time.Hour) // the freeze: 60 unobserved minutes
	tick(m)
	noVerdict(t, m, "the first tick after the gap")

	// From here the tool NEVER ends and the supervisor observes minute
	// after minute: the bound must be reached again, in observed time.
	var dead *supervise.StepDead
	for i := 0; i < 31; i++ {
		clk.Advance(time.Minute)
		tick(m)
		for _, v := range m.link.Verdicts.Drain() {
			if d, ok := v.(supervise.StepDead); ok {
				dead = &d
			}
		}
		if dead != nil {
			break
		}
	}
	if dead == nil || dead.Cause != supervise.CauseToolHung {
		t.Fatalf("an observed 30m tool hang after a freeze must be judged tool_hung; got %+v", dead)
	}
}
