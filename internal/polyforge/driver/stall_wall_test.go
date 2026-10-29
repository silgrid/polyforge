package driver

// stall_wall_test.go ( R4): the supervisor forgives its own
// observation gap — a host or daemon freeze — by shifting the dispatch
// tracker's stamps, so a stalled dispatch is not judged
// dead for the time nobody watched. The driver's T ledger must agree: the
// dispatch's booked wall (BudgetConsumed.WallMs, and through it the next
// dispatch's PriorRuntime) excludes the gap the supervisor declared.
// Otherwise a 60-90 minute freeze lands in T whole and the next dispatch
// of the same step starts with PriorRuntime >= the freeze, so step_timeout
// fires at once.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// stallScene runs step s: dispatch 1 holds; the test plays the supervisor
// (attaches dispatch 1's step_started to its tracker, as the monitor's
// onStepStarted does), jumps the clock 61 minutes (a host freeze), lets
// the supervisor's first tick after it declare the 60 unwatched minutes
// when declare is set (ObserveTick with the monitor's cutoff/shift), and
// then ends dispatch 1 with end. It answers the env and every step_started
// fact the driver sent.
func stallScene(t *testing.T, first fakeEntry, declare bool, end func(env *supEnv)) (*supEnv, []supervise.StepStarted) {
	t.Helper()
	base := time.Unix(1_000_000, 0)
	var mu sync.Mutex
	now := base
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	env := newSupEnv(t, func(c *Config) { c.Now = clock }, spec("s"))
	env.link.Now = clock
	env.sp.script = []fakeEntry{first, done(`{}`)}

	var factsMu sync.Mutex
	var started []supervise.StepStarted
	collect := func() {
		factsMu.Lock()
		defer factsMu.Unlock()
		for _, f := range env.link.Facts.Drain() {
			if s, ok := f.(supervise.StepStarted); ok {
				started = append(started, s)
			}
		}
	}
	go func() {
		waitForSpawns(env, 1)
		var first supervise.StepStarted
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
			collect()
			factsMu.Lock()
			if len(started) > 0 {
				first = started[0]
			}
			factsMu.Unlock()
			if first.SpawnN == 1 {
				break
			}
		}
		tr := env.link.Tracker(1)
		tr.Begin(first)
		mu.Lock()
		now = base.Add(61 * time.Minute) // the freeze
		mu.Unlock()
		if declare {
			// the monitor's observeGap after a 61m gap at a 1m poll:
			// cutoff = last tick + poll, shift = gap - poll
			tr.ObserveTick(base.Add(time.Minute), 60*time.Minute)
		} else {
			tr.ObserveTick(time.Time{}, 0)
		}
		end(env)
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	collect()
	factsMu.Lock()
	defer factsMu.Unlock()
	return env, append([]supervise.StepStarted(nil), started...)
}

func TestDeclaredSupervisorPauseIsNotBookedIntoT(t *testing.T) {
	dies := func(env *supEnv) {
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseHarnessSilent, Evidence: "silent after the freeze"})
	}
	t.Run("a dispatch that died after the freeze", func(t *testing.T) {
		env, started := stallScene(t, fakeEntry{hold: true}, true, dies)
		if len(started) != 2 {
			t.Fatalf("the dead dispatch must be re-dispatched once: %d step_started facts", len(started))
		}
		// 61 wall minutes, 60 of them declared unwatched: T carries 1m.
		if prior := started[1].Thresholds.PriorRuntime; prior != time.Minute {
			t.Fatalf("the successor's PriorRuntime %v must exclude the declared 60m gap (want 1m)", prior)
		}
		if wall := env.exec(t).Steps["s"].BudgetConsumed.WallMs; wall != time.Minute.Milliseconds() {
			t.Fatalf("the booked wall %dms must exclude the declared gap (want 60000)", wall)
		}
	})
	t.Run("a dispatch that delivered after the freeze", func(t *testing.T) {
		delivers := func(env *supEnv) {
			env.sp.mu.Lock()
			run := env.sp.runs[0]
			env.sp.mu.Unlock()
			_ = run.Terminate(0) // releases the held Wait: the envelope lands
		}
		held := done(`{}`)
		held.hold = true
		env, _ := stallScene(t, held, true, delivers)
		if wall := env.exec(t).Steps["s"].BudgetConsumed.WallMs; wall != time.Minute.Milliseconds() {
			t.Fatalf("the booked wall %dms must exclude the declared gap (want 60000)", wall)
		}
	})
	t.Run("control: no declared gap books the whole wall", func(t *testing.T) {
		_, started := stallScene(t, fakeEntry{hold: true}, false, dies)
		if len(started) != 2 || started[1].Thresholds.PriorRuntime != 61*time.Minute {
			t.Fatalf("without a declared gap the whole 61m stays in T: %+v", started)
		}
	})
}
