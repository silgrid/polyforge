package driver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
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
	d.cfg.StepEnded = func(ctx context.Context) error {
		e := readExec(t, execPath)
		st := e.Steps[e.CurrentStep]
		got = append(got, seen{e.CurrentStep, st.Dispatch})
		return nil
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
	d.cfg.StepEnded = func(context.Context) error { calls++; return nil }
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
	d.cfg.StepEnded = func(context.Context) error { at = at.Add(hook); return nil }
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

// polyforge#200 (Q5 B): a step-end cleanup that cannot prove the step's
// processes gone parks the work item — the accepted hand-off stays on the
// call ledger, nothing is written as the step's outcome, no successor
// dispatches — and the resume re-runs the hook before settling that kept
// hand-off: it parks again while the sweep still fails and advances
// without re-dispatching the step once it proves clean.
func TestStepEndedCleanupFailureParksKeepsHandoff(t *testing.T) {
	d, sp, _, dir := handoffEnv(t, 4, spec("collect"), spec("close"))
	sp.script = []fakeEntry{
		handedOff(&stepcall.Handoff{Status: stepcall.StatusDone}, func() {
			appendLedgerHandoff(t, dir, "collect", 1, stepcall.StatusDone)
		}),
		done(`{}`), // close: reachable only if the lane wrongly advances
	}
	d.cfg.StepEnded = func(context.Context) error {
		return errors.New("step-end sweep incomplete: pids 4242, 4243 still running")
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := d.Snapshot()
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("an incomplete step-end cleanup must park the work item, status %q", x.WorkflowStatus)
	}
	for _, want := range []string{"cleanup failed", "4242", "pf resume"} {
		if !strings.Contains(x.BlockedReason, want) {
			t.Fatalf("the park's reason must carry %q, got %q", want, x.BlockedReason)
		}
	}
	if st := x.Steps["collect"]; st.Status != capsule.StepInProgress || st.Dispatch != 1 {
		t.Fatalf("the park keeps the step on its dispatch for the resume to settle: %+v", st)
	}
	if got := sp.dispatched(); len(got) != 1 || !strings.HasPrefix(got[0], "collect@") {
		t.Fatalf("no successor may dispatch on an incomplete cleanup: %v", got)
	}

	// the resume re-runs the hook before settling the kept hand-off: a
	// sweep that still fails keeps the park.
	hookCalls := 0
	sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
	d2, err := New(Config{WI: d.wi, ExecPath: d.execPath, Workflow: d.workflow, Spawner: sp2,
		Log: &eventLog{}, HubWrites: &hubSink{sp: sp2}})
	if err != nil {
		t.Fatal(err)
	}
	d2.cfg.StepEnded = func(context.Context) error {
		hookCalls++
		return errors.New("still incomplete")
	}
	if err := d2.ResumeCurrent(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("resumed Run: %v", err)
	}
	if hookCalls != 1 {
		t.Fatalf("the resume must re-run the step-end hook before settling, ran %d time(s)", hookCalls)
	}
	if x := d2.Snapshot(); x.WorkflowStatus != StatusBlocked || x.Steps["collect"].Status != capsule.StepInProgress {
		t.Fatalf("a still-failing sweep keeps the park: %q %+v", x.WorkflowStatus, x.Steps["collect"])
	}
	if got := sp2.dispatched(); len(got) != 0 {
		t.Fatalf("nothing may dispatch while the cleanup is unproven: %v", got)
	}

	// a sweep that now proves clean settles the KEPT hand-off (no
	// re-dispatch of collect) and advances.
	sp3 := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
	d3, err := New(Config{WI: d.wi, ExecPath: d.execPath, Workflow: d.workflow, Spawner: sp3,
		Log: &eventLog{}, HubWrites: &hubSink{sp: sp3}})
	if err != nil {
		t.Fatal(err)
	}
	d3.cfg.StepEnded = func(context.Context) error { return nil }
	if err := d3.ResumeCurrent(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := d3.Run(context.Background()); err != nil {
		t.Fatalf("resumed Run: %v", err)
	}
	if got := sp3.dispatched(); len(got) != 1 || !strings.HasPrefix(got[0], "close@") {
		t.Fatalf("the kept hand-off settles and the successor runs, without re-dispatching collect: %v", got)
	}
	if x := d3.Snapshot(); x.WorkflowStatus != StatusCompleted || x.Steps["collect"].Status != capsule.StepDone {
		t.Fatalf("the lane completes: %q %+v", x.WorkflowStatus, x.Steps["collect"])
	}
}
