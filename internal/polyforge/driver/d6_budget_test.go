package driver

// d6_budget_test.go: the D6 budget semantics of polyforge#147 — the
// step's total runtime T accumulates over the WHOLE step (no window is
// reopened by a candidate switch or a human continue; only a rewind's
// fresh start zeroes it), and every timeout-class death relaxes the
// step's wall budget x2 at disposal time, persisted in execution.json.
//
// Written RED against the pre-change driver (slow-model timeouts
// relaxed nothing and a switch rebased the T window), GREEN after.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// timeoutOnce drives one timeout-class death of the given cause,
// followed by one healthy dispatch, over a step with the given chain.
func timeoutOnce(t *testing.T, cause supervise.Cause, wall time.Duration, models ...string) *supEnv {
	t.Helper()
	env := newChainEnv(t, func(c *Config) {
		c.Limits = supervise.Defaults{StepTimeout: wall}
	}, "s", models...)
	env.sp.script = []fakeEntry{{hold: true}, done(`{"ok":true}`)}
	go func() {
		waitForSpawns(env, 1)
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: cause, Evidence: "T exhausted"})
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return env
}

// TestD6SlowModelTimeoutRelaxesWallAndSwitches: a slow-model timeout
// switches to the next candidate AND doubles the step's wall budget,
// persisting it in execution.json.
func TestD6SlowModelTimeoutRelaxesWallAndSwitches(t *testing.T) {
	env := timeoutOnce(t, supervise.CauseStepTimeoutSlowModel, 6*time.Second, "a", "b")
	if m := env.models(); len(m) != 2 || m[0] != "a" || m[1] != "b" {
		t.Fatalf("a slow-model timeout switches candidates: %v", m)
	}
	sup := env.exec(t).Steps["s"].Supervision
	if sup == nil || sup.RelaxedWallMs != (12*time.Second).Milliseconds() {
		t.Fatalf("the slow-model disposal must relax T to 12000ms: %+v", sup)
	}
}

// TestD6SlowModelRelaxationDoublesEachTime: every consecutive
// slow-model timeout doubles the previous bound (6s -> 12s -> 24s).
func TestD6SlowModelRelaxationDoublesEachTime(t *testing.T) {
	env := newChainEnv(t, func(c *Config) {
		c.Limits = supervise.Defaults{StepTimeout: 6 * time.Second}
	}, "s", "a", "b", "c")
	env.sp.script = []fakeEntry{{hold: true}, {hold: true}, done(`{"ok":true}`)}
	go func() {
		for n := int64(1); n <= 2; n++ {
			waitForSpawns(env, int(n))
			env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: n, Cause: supervise.CauseStepTimeoutSlowModel, Evidence: "T exhausted"})
		}
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sup := env.exec(t).Steps["s"].Supervision
	if sup == nil || sup.RelaxedWallMs != (24*time.Second).Milliseconds() {
		t.Fatalf("two relaxations must land 24000ms (6s doubled twice): %+v", sup)
	}
}

// TestD6LongTaskRelaxesSameModelThenBlocks: a long-task timeout relaxes
// T x2 and retries the SAME model; the second one doubles T again and
// blocks the step.
func TestD6LongTaskRelaxesSameModelThenBlocks(t *testing.T) {
	env := newChainEnv(t, func(c *Config) {
		c.Limits = supervise.Defaults{StepTimeout: 3 * time.Second}
	}, "s", "a", "b")
	env.sp.script = []fakeEntry{{hold: true}, {hold: true}}
	go func() {
		for n := int64(1); n <= 2; n++ {
			waitForSpawns(env, int(n))
			env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: n, Cause: supervise.CauseStepTimeoutLongTask, Evidence: "long tool run"})
		}
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := env.models(); len(m) != 2 || m[0] != m[1] {
		t.Fatalf("a long-task timeout retries the SAME model: %v", m)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("the second long-task timeout blocks: %q", x.WorkflowStatus)
	}
	// the blocking (second) timeout doubles T again before it parks, so
	// a human continue runs under a fresh window: 3s -> 6s -> 12s
	if sup := x.Steps["s"].Supervision; sup == nil || sup.RelaxedWallMs != (12*time.Second).Milliseconds() {
		t.Fatalf("the relaxed bound must persist (3s doubled twice): %+v", x.Steps["s"].Supervision)
	}
}

// TestD6NonTimeoutDeathsNeverRelax: only timeout-class deaths relax the
// wall budget — a model-class or plain death must leave T alone.
func TestD6NonTimeoutDeathsNeverRelax(t *testing.T) {
	for _, cause := range []string{FailExitModel, FailExitOther} {
		env := failOnce(t, cause, "a", "b")
		st := env.exec(t).Steps["s"]
		if st.Supervision != nil && st.Supervision.RelaxedWallMs != 0 {
			t.Fatalf("%s relaxed T to %dms — only timeout-class deaths relax", cause, st.Supervision.RelaxedWallMs)
		}
	}
}

// TestD6SuccessorRunsUnderTheRelaxedBound: after the slow-model
// disposal the successor's thresholds carry the DOUBLED bound with the
// predecessor's runtime carried over — the cumulative T is strictly
// below the relaxed bound, so the newcomer is never judged dead at its
// own startup (the spec's named scenario).
func TestD6SuccessorRunsUnderTheRelaxedBound(t *testing.T) {
	tick := 0
	env := newChainEnv(t, func(c *Config) {
		c.Limits = supervise.Defaults{StepTimeout: 6 * time.Second}
		c.Now = func() time.Time {
			tick++
			return time.Unix(1_000_000, 0).Add(time.Duration(tick) * 5 * time.Second)
		}
	}, "s", "a", "b")
	env.sp.script = []fakeEntry{{hold: true}, done(`{"ok":true}`)}
	go func() {
		waitForSpawns(env, 1)
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseStepTimeoutSlowModel, Evidence: "T exhausted"})
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var started []supervise.StepStarted
	for _, f := range env.link.Facts.Drain() {
		if s, ok := f.(supervise.StepStarted); ok {
			started = append(started, s)
		}
	}
	if len(started) != 2 {
		t.Fatalf("step_started facts: %d", len(started))
	}
	th := started[1].Thresholds
	if th.StepTimeout != 12*time.Second {
		t.Fatalf("the successor must run under the DOUBLED bound: %v", th.StepTimeout)
	}
	if th.PriorRuntime <= 0 {
		t.Fatalf("T is cumulative: the successor inherits the spent runtime, got %v", th.PriorRuntime)
	}
	if th.PriorRuntime >= th.StepTimeout {
		t.Fatalf("the successor must have real budget left (%v spent of %v)", th.PriorRuntime, th.StepTimeout)
	}
}

// TestD6RewindZeroesTheStepBudget: the only zeroing of T is a rewind's
// fresh start of the step (the archived attempt keeps the old window's
// accounting).
func TestD6RewindZeroesTheStepBudget(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"), gateSpec("g", "a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: completedResult("a", 1, `{}`), WallMs: 5000,
	}); err != nil {
		t.Fatalf("result: %v", err)
	}
	mustStart(t, d, "g", 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "g", SpawnN: 2,
		Result: &StepResult{StepID: "g", SpawnN: 2, Outcome: OutcomeGateFailed},
	}); err != nil {
		t.Fatalf("gate fail: %v", err)
	}
	mustStart(t, d, "a", 3)
	st := readExec(t, execPath).Steps["a"]
	if st.BudgetConsumed.WallMs != 0 {
		t.Fatalf("a rewound rerun starts from a zeroed T: %+v", st.BudgetConsumed)
	}
	var archived int64
	for _, a := range st.Attempts {
		if a.ConsumedBudget != nil {
			archived = a.ConsumedBudget.WallMs
		}
	}
	if archived != 5000 {
		t.Fatalf("the archived attempt keeps the old window's accounting: %d", archived)
	}
}

// TestD6GateFailRewindZeroesTheGateBudget: the gate step stays
// in_progress across its own FAIL rewind, so its T must be reset by the
// rewind itself — its re-entry is a fresh window (D6: reset on rewind).
func TestD6GateFailRewindZeroesTheGateBudget(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"), gateSpec("g", "a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: completedResult("a", 1, `{}`),
	}); err != nil {
		t.Fatalf("result: %v", err)
	}
	mustStart(t, d, "g", 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "g", SpawnN: 2, WallMs: 7000,
		Result: &StepResult{StepID: "g", SpawnN: 2, Outcome: OutcomeGateFailed},
	}); err != nil {
		t.Fatalf("gate fail: %v", err)
	}
	if got := readExec(t, execPath).Steps["g"].BudgetConsumed.WallMs; got != 0 {
		t.Fatalf("a gate-fail rewind must zero the gate's own T, got %dms", got)
	}
}

// repo review 2026-10-16 B-25: the accepted attempt takes the max-seq
// ordinal (the ledger can hold gaps) and the driver's injected clock.
func TestAcceptedAttemptSeqAndClock(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	d.mu.Lock()
	d.exec.Steps["a"].Attempts = []capsule.Attempt{{Cause: CauseInfraFailure, AttemptSeq: 1}, {Cause: CauseInfraFailure, AttemptSeq: 3}}
	d.mu.Unlock()
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: completedResult("a", 1, `{}`),
	}); err != nil {
		t.Fatalf("result: %v", err)
	}
	at := readExec(t, execPath).Steps["a"].Attempts
	last := at[len(at)-1]
	if last.Cause != "accepted" || last.AttemptSeq != 4 {
		t.Fatalf("accepted attempt = %+v, want seq 4", last)
	}
	if !strings.HasPrefix(last.ArchivedAt, "2026-09-20T") {
		t.Fatalf("accepted attempt archived at %q, want the driver's (fake) clock", last.ArchivedAt)
	}
}

// TestD6CancelledDispatchBooksNothing: the cancelled driver (pause,
// recovery relaunch) books no wall time and writes nothing; a system
// relaunch's dispatch is booked by the successor's interrupted archive
// (TestD6HangRelaunchLoopReachesT).
func TestD6CancelledDispatchBooksNothing(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("a"))
	var mu sync.Mutex
	at := time.Unix(1_700_000_000, 0)
	d.cfg.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		at = at.Add(time.Second)
		return at
	}
	sp.script = []fakeEntry{{hold: true}}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	before := readExec(t, execPath).Version
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v, want the cancellation", err)
	}
	exec := readExec(t, execPath)
	if exec.Version != before {
		t.Fatalf("a cancelled dispatch wrote execution.json: version %d -> %d", before, exec.Version)
	}
	if got := exec.Steps["a"].BudgetConsumed.WallMs; got != 0 {
		t.Fatalf("a cancelled dispatch booked %dms", got)
	}
	for _, k := range d.log.(*eventLog).kinds() {
		if k == "budget_update" {
			t.Fatal("a cancelled dispatch emitted budget_update")
		}
	}
}

// interruptionCycles runs one driver per entry of interrupted over ONE
// capsule: each dispatches step "s", holds it for hold on a shared fake
// clock and is cancelled (the runtime's Terminate); gap passes before
// the next driver starts (the pause, the recovery). The entry is the
// Config.Interrupted the driver starts with (nil: a claim, e.g. the
// resume after a pause). It answers the last driver's link.
func interruptionCycles(t *testing.T, stepTimeout, hold, gap time.Duration, interrupted []*InterruptedDispatch) (string, *supervise.Link) {
	t.Helper()
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	var mu sync.Mutex
	clock := time.Unix(1_700_000_000, 0)
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	var link *supervise.Link
	for i, in := range interrupted {
		sp := &fakeSpawner{script: []fakeEntry{{hold: true}}}
		link = supervise.NewLink("wi_run")
		d, err := New(Config{
			WI:          WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
			ExecPath:    execPath,
			Workflow:    &Workflow{Steps: []StepSpec{spec("s")}, MaxTotal: 4, MaxPerStep: 2},
			Spawner:     sp,
			Log:         &safeLog{},
			Link:        link,
			Limits:      supervise.Defaults{StepTimeout: stepTimeout},
			Compose:     composeOf(map[string][]string{"s": {"pi:a"}}),
			Interrupted: in,
			Now:         now,
		})
		if err != nil {
			t.Fatalf("cycle %d: New: %v", i, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- d.Run(ctx) }()
		waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
		mu.Lock()
		clock = clock.Add(hold)
		mu.Unlock()
		cancel()
		if err := <-errCh; !errors.Is(err, context.Canceled) {
			t.Fatalf("cycle %d: Run: %v, want the cancellation", i, err)
		}
		mu.Lock()
		clock = clock.Add(gap)
		mu.Unlock()
	}
	return execPath, link
}

// TestD6PauseCyclesBookNothing: a dispatch cancelled by an operator
// pause books no wall time — whether the work item resumes by a fresh
// claim (no interrupted archive) or by a live-holder operator resume (an
// archive the bridge marks non-booking) — so neither the dispatch nor
// the pause itself ever counts toward T.
func TestD6PauseCyclesBookNothing(t *testing.T) {
	resume := &InterruptedDispatch{Detail: "operator resume"}
	execPath, _ := interruptionCycles(t, time.Hour, 20*time.Minute, time.Hour,
		[]*InterruptedDispatch{nil, nil, resume, nil, resume, resume})
	exec := readExec(t, execPath)
	if got := exec.Steps["s"].BudgetConsumed.WallMs; got != 0 {
		t.Fatalf("pause cycles booked %dms toward T", got)
	}
	if got := exec.BudgetConsumed.WallMs; got != 0 {
		t.Fatalf("pause cycles booked %dms on the workflow aggregate", got)
	}
}

// TestD6HangRelaunchLoopReachesT: a dispatch the system relaunches after
// a hang books its runtime in the successor's interrupted archive, so a
// hang→relaunch loop accumulates and the next dispatch starts with T
// already spent (the supervisor's T rule then ends it at once). The
// booking runs from the dispatch's start to the successor's archive
// write, so the recovery between the kill and the relaunch counts too.
func TestD6HangRelaunchLoopReachesT(t *testing.T) {
	stalled := &InterruptedDispatch{Detail: "driver_stalled", BookWall: true}
	execPath, link := interruptionCycles(t, 10*time.Minute, 3*time.Minute, time.Minute,
		[]*InterruptedDispatch{nil, stalled, stalled, stalled})
	exec := readExec(t, execPath)
	if got := exec.Steps["s"].BudgetConsumed.WallMs; got != (12 * time.Minute).Milliseconds() {
		t.Fatalf("three relaunched dispatches of 3m (+1m recovery each) booked %dms, want 12m", got)
	}
	var last *supervise.StepStarted
	for _, f := range link.Facts.Drain() {
		if s, ok := f.(supervise.StepStarted); ok {
			last = &s
		}
	}
	if last == nil {
		t.Fatal("the last relaunch dispatched no step")
	}
	if th := last.Thresholds; th.PriorRuntime < th.StepTimeout {
		t.Fatalf("after the loop the dispatch must start with T spent: %v of %v", th.PriorRuntime, th.StepTimeout)
	}
}

// TestD6RelaunchBeforeTheNextDispatchBooksOnce: two relaunches with no
// dispatch between them book the interrupted dispatch once, plus only
// the time since the first archive.
func TestD6RelaunchBeforeTheNextDispatchBooksOnce(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"))
	at := time.Unix(1_700_000_000, 0)
	d.cfg.Now = func() time.Time { return at }
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	at = at.Add(3 * time.Second)
	relaunch := TransitionRequest{Kind: TransInterrupted, Reason: "driver_stalled", BookWall: true, SpawnN: 1}
	if err := d.transition(relaunch); err != nil {
		t.Fatalf("first archive: %v", err)
	}
	at = at.Add(2 * time.Second)
	if err := d.transition(relaunch); err != nil {
		t.Fatalf("second archive: %v", err)
	}
	if got := readExec(t, execPath).Steps["a"].BudgetConsumed.WallMs; got != 5000 {
		t.Fatalf("booked %dms, want 3s + 2s (no re-booking of the dispatch)", got)
	}
}

// TestD6PausedDispatchNeverBookedByALaterRelaunch: pause → resume by a
// fresh claim → the resumed driver stalls before any new dispatch → the
// system relaunches. The paused dispatch is never booked: the claim
// re-stamped the step's start, so the relaunch books only from the claim.
func TestD6PausedDispatchNeverBookedByALaterRelaunch(t *testing.T) {
	execPath, _ := interruptionCycles(t, time.Hour, 20*time.Minute, time.Hour, []*InterruptedDispatch{nil})
	dir := filepath.Dir(execPath)
	at := time.Unix(1_700_000_000, 0).Add(5 * time.Hour)
	newDriver := func(in *InterruptedDispatch, link *supervise.Link, sp *fakeSpawner) *Driver {
		d, err := New(Config{
			WI:          WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
			ExecPath:    execPath,
			Workflow:    &Workflow{Steps: []StepSpec{spec("s")}, MaxTotal: 4, MaxPerStep: 2},
			Spawner:     sp,
			Log:         &safeLog{},
			Link:        link,
			Limits:      supervise.Defaults{StepTimeout: time.Hour},
			Compose:     composeOf(map[string][]string{"s": {"pi:a"}}),
			Interrupted: in,
			Now:         func() time.Time { return at },
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d
	}

	// the resume: a fresh claim whose driver stalls before dispatching
	link := supervise.NewLink("wi_run")
	link.Verdict(supervise.DriverStalled{Duration: time.Minute, Phase: "between_steps"})
	sp := &fakeSpawner{}
	if err := newDriver(nil, link, sp).Run(context.Background()); !errors.Is(err, ErrDriverStalled) {
		t.Fatalf("claim driver: %v, want the stall", err)
	}
	if len(sp.dispatched()) != 0 {
		t.Fatal("the claim driver must stall before any dispatch")
	}

	// the system relaunch: its archive books nothing of the paused dispatch
	link = supervise.NewLink("wi_run")
	link.Verdict(supervise.DriverStalled{Duration: time.Minute, Phase: "between_steps"})
	stalled := &InterruptedDispatch{Detail: "driver_stalled", BookWall: true}
	if err := newDriver(stalled, link, &fakeSpawner{}).Run(context.Background()); !errors.Is(err, ErrDriverStalled) {
		t.Fatalf("relaunched driver: %v, want the stall", err)
	}
	if got := readExec(t, execPath).Steps["s"].BudgetConsumed.WallMs; got != 0 {
		t.Fatalf("a relaunch after a pause booked %dms of the paused dispatch", got)
	}
}

// TestD6CumulativeTimeoutRotatesOnlyForTheDyingDispatch reproduces the two
// production shapes of D1 end to end through the decision: the
// supervisor's own rule 4 judges the dispatch's snapshot, and the driver
// disposes of that verdict. The shape (a 12m38s tail of a step
// that spent 1h17m22s before it) must NOT switch the model — it is the
// step's length, so the same candidate resumes its conversation under the
// doubled T; the shape (the whole T in one dispatch, mostly
// waiting on the model) MUST switch, fresh.
func TestD6CumulativeTimeoutRotatesOnlyForTheDyingDispatch(t *testing.T) {
	const T = 90 * time.Minute
	cases := []struct {
		name                     string
		prior, age, tools, model time.Duration
		wantCause                string
		wantModels               []string
		wantResume               bool
		wantLongTask             int
	}{
		{" code attempt 4", 77*time.Minute + 22*time.Second, 12*time.Minute + 38*time.Second,
			30 * time.Second, 12*time.Minute + 2*time.Second,
			FailStepTimeoutLongTask, []string{"a", "a"}, true, 1},
		{" code attempt 1", 0, 90 * time.Minute, 31 * time.Minute, 59*time.Minute + time.Second,
			FailStepTimeoutSlowModel, []string{"a", "b"}, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(10_000_000, 0)
			verdict := supervise.Judge(supervise.Snapshot{
				Step: "s", SpawnN: 1, Begun: true, StartedAt: now.Add(-tc.age),
				Thresholds: supervise.Thresholds{StepTimeout: T, PriorRuntime: tc.prior},
				ModelTime:  tc.model, ToolTime: tc.tools,
			}, now, 0)
			if verdict == nil {
				t.Fatal("setup: rule 4 did not fire on an expired T")
			}
			env := newChainEnv(t, func(c *Config) {
				c.Limits = supervise.Defaults{StepTimeout: T}
			}, "s", "a", "b")
			env.sp.script = []fakeEntry{{hold: true}, done(`{"ok":true}`)}
			go func() {
				waitForSpawns(env, 1)
				env.link.Verdict(*verdict)
			}()
			if err := env.d.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if m := env.models(); strings.Join(m, ",") != strings.Join(tc.wantModels, ",") {
				t.Fatalf("dispatched models %v, want %v (%s)", m, tc.wantModels, verdict.Evidence)
			}
			st := env.exec(t).Steps["s"]
			var cause string
			for _, a := range st.Attempts {
				if a.Cause == CauseInfraFailure {
					cause = a.FailureCause
				}
			}
			if cause != tc.wantCause {
				t.Fatalf("archived failure_cause %q, want %q", cause, tc.wantCause)
			}
			if sp := env.sp.spawnAt(1); sp.Resume != tc.wantResume || sp.Fresh == tc.wantResume {
				t.Fatalf("retry shape resume=%v fresh=%v, want resume=%v", sp.Resume, sp.Fresh, tc.wantResume)
			}
			sup := st.Supervision
			if sup == nil || sup.RelaxedWallMs != (2*T).Milliseconds() || sup.LongTaskTimeouts != tc.wantLongTask {
				t.Fatalf("supervision %+v, want T relaxed to %v and %d long-task timeouts", sup, 2*T, tc.wantLongTask)
			}
		})
	}
}

// TestD6SlowModelRotatesAtMostOncePerTimeoutLedger ( S3):
// "a T era switches the candidate on a timeout at most once" is enforced
// by the driver, not left to timing (whether the successor's tick
// overshoot beats its predecessor's teardown and so hands it half of the
// doubled T). A second slow-model timeout before the step's timeout ledger
// resets is archived as step_timeout_long_task: the SAME candidate retries
// under the doubled T, and the long-task counter starts.
func TestD6SlowModelRotatesAtMostOncePerTimeoutLedger(t *testing.T) {
	env := newChainEnv(t, func(c *Config) {
		c.Limits = supervise.Defaults{StepTimeout: 6 * time.Second}
	}, "s", "a", "b", "c")
	env.sp.script = []fakeEntry{{hold: true}, {hold: true}, done(`{"ok":true}`)}
	go func() {
		for n := int64(1); n <= 2; n++ {
			waitForSpawns(env, int(n))
			env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: n, Cause: supervise.CauseStepTimeoutSlowModel, Evidence: "T exhausted"})
		}
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := env.models(); len(m) != 3 || m[0] == m[1] || m[1] != m[2] {
		t.Fatalf("the first slow-model timeout switches, the second retries the same candidate: %v", m)
	}
	st := env.exec(t).Steps["s"]
	var causes []string
	for _, a := range st.Attempts {
		if a.Cause == CauseInfraFailure {
			causes = append(causes, a.FailureCause)
		}
	}
	if len(causes) != 2 || causes[0] != FailStepTimeoutSlowModel || causes[1] != FailStepTimeoutLongTask {
		t.Fatalf("the second slow-model timeout is archived as long_task: %v", causes)
	}
	if !strings.Contains(st.Attempts[len(st.Attempts)-2].Detail, "already rotated") {
		t.Fatalf("the archived detail must say why it was not a second rotation: %q", st.Attempts[len(st.Attempts)-2].Detail)
	}
	// the rule reads the persisted counters: pin the shape it depends on
	if sup := st.Supervision; sup == nil || sup.SlowModelTimeouts != 1 || sup.LongTaskTimeouts != 1 {
		t.Fatalf("persisted supervision after one rotation and one re-classified timeout: want slow_model_timeouts=1 long_task_timeouts=1, got %+v", sup)
	}
}
