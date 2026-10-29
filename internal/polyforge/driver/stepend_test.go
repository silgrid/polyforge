package driver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// The step-end hook (the daemon's task-repo WIP push) runs once per
// dispatch whose process is gone — a delivered result and a failed
// dispatch alike — before that dispatch's result or failure is written,
// and never for a dispatch the context cancelled.
func TestStepEndedRunsAfterEachDispatchBeforeItsWrite(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("a"), spec("b"))
	sp.script = []fakeEntry{
		done(`{"summary":"A"}`),
		{err: errors.New("step process exited 1")},
		done(`{"summary":"B"}`),
	}
	// Step.Dispatch names the ordinal awaiting its result; the write that
	// settles the dispatch clears it.
	type seen struct {
		step     string
		dispatch int64
	}
	var got []seen
	d.cfg.StepEnded = func(ctx context.Context) {
		e := readExec(t, execPath)
		st := e.Steps[e.CurrentStep]
		got = append(got, seen{e.CurrentStep, st.Dispatch})
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []seen{{"a", 1}, {"b", 2}, {"b", 3}}
	if len(got) != len(want) {
		t.Fatalf("hook ran %d times (%+v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hook call %d saw %+v, want %+v (it must run before the dispatch's write)", i, got[i], want[i])
		}
	}
}

func TestStepEndedSkippedForACancelledDispatch(t *testing.T) {
	d, sp, _ := runEnv(t, spec("a"))
	sp.script = []fakeEntry{{hold: true}}
	calls := 0
	d.cfg.StepEnded = func(context.Context) { calls++ }
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v, want the cancellation", err)
	}
	if calls != 0 {
		t.Fatalf("the hook ran %d time(s) for a cancelled dispatch", calls)
	}
}

// TestStepEndCleanupIsNotChargedToStepT: the step-end hook is cleanup,
// not dispatch runtime. A slow WIP push (StepEnded) must not be booked
// into the step's cumulative T, or the previous dispatch's cleanup becomes
// the next dispatch's timeout (the step-end escapee sweep
// pushed a retry over a tiny T).
func TestStepEndCleanupIsNotChargedToStepT(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("a"))
	at := time.Unix(1_700_000_000, 0)
	d.cfg.Now = func() time.Time { at = at.Add(10 * time.Millisecond); return at }
	link := supervise.NewLink("wi_run")
	d.cfg.Link = link
	const hook = 10 * time.Minute
	d.cfg.StepEnded = func(context.Context) { at = at.Add(hook) }
	sp.script = []fakeEntry{{err: errors.New("step process exited 1")}, done(`{"summary":"A"}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var starts []supervise.StepStarted
	for _, f := range link.Facts.Drain() {
		if s, ok := f.(supervise.StepStarted); ok {
			starts = append(starts, s)
		}
	}
	if len(starts) != 2 {
		t.Fatalf("step_started facts: %d, want 2", len(starts))
	}
	if prior := starts[1].Thresholds.PriorRuntime; prior > time.Minute {
		t.Fatalf("second dispatch prior runtime %s: the step-end hook was charged to T", prior)
	}
	if got := readExec(t, execPath).Steps["a"].BudgetConsumed.WallMs; got > time.Minute.Milliseconds() {
		t.Fatalf("step booked %dms: the step-end hooks are not dispatch runtime", got)
	}
}
