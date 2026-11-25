package driver

// lease_interval_test.go (polyforge#245 245-lane): the lane-declared
// landing-lock interval in the driver. The lease is taken before the
// enter step first dispatches and held across the interval's dispatches;
// a lost lease releases the group and rewinds to the declared enter step
// charged to no rewind budget; an ask releases it and the answer
// re-enters at enter; leaving the interval (the exit step's next step, or
// the workflow's end) releases it. The server lease itself is the daemon
// manager's (daemon/landing_test.go).

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// fakeLander is one interval's IntervalLeases: it counts the holds and
// releases and answers a lost lease once told to.
type fakeLander struct {
	mu       sync.Mutex
	holds    int
	releases int
	lost     error
	sticky   bool
	losings  int
	queued   bool
	// held records whether the group is currently held.
	held bool
	// keys is the group's held-key list the events read (polyforge#249);
	// nil keeps the pre-#249 shape for the older tests.
	keys []LeaseKey
	// cycleN makes every hold's non-secret cycle id distinct, as the
	// production manager's is.
	cycleN int
	cycle  string
}

func (f *fakeLander) Hold(_ context.Context, _, _ string, _ []string, queued func(LeaseQueue)) (IntervalLease, error) {
	f.mu.Lock()
	f.holds++
	f.cycleN++
	f.cycle = "lc_test_" + strconv.Itoa(f.cycleN)
	f.held = true
	announce := f.queued
	keys := append([]LeaseKey(nil), f.keys...)
	f.mu.Unlock()
	if announce && queued != nil {
		q := LeaseQueue{Position: 3, Since: time.Now(), Cycle: f.cycle}
		if len(keys) > 0 {
			q.Repo, q.Branch, q.Position = keys[0].Repo, keys[0].Branch, keys[0].Position
		}
		queued(q)
	}
	return f, nil
}

// Cycle and Keys are the non-secret event face (polyforge#249).
func (f *fakeLander) Cycle() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cycle
}

func (f *fakeLander) Keys() []LeaseKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]LeaseKey(nil), f.keys...)
}

// Err answers a lost lease once (the next hold is healthy again).
func (f *fakeLander) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := f.lost
	f.losings++
	if !f.sticky || f.losings > 2 {
		f.lost = nil
	}
	return err
}

func (f *fakeLander) Check(context.Context) error { return f.Err() }

func (f *fakeLander) Release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held {
		f.releases++
	}
	f.held = false
}

func (f *fakeLander) lose(err error) {
	f.mu.Lock()
	f.lost = err
	f.mu.Unlock()
}

// clear makes the lander healthy again.
func (f *fakeLander) clear() {
	f.mu.Lock()
	f.lost, f.sticky = nil, false
	f.mu.Unlock()
}

// loseSticky keeps answering the loss (a renewal that stays failed).
func (f *fakeLander) loseSticky(err error) {
	f.mu.Lock()
	f.lost, f.sticky = err, true
	f.mu.Unlock()
}

func (f *fakeLander) counts() (holds, releases int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.holds, f.releases
}

func landingEnv(t *testing.T, enter, exit string, steps ...StepSpec) (*Driver, *fakeSpawner, *eventLog, *fakeLander) {
	t.Helper()
	return landingEnvAt(t, t.TempDir(), "", enter, exit, steps...)
}

func landingEnvAt(t *testing.T, dir, execPath, enter, exit string, steps ...StepSpec) (*Driver, *fakeSpawner, *eventLog, *fakeLander) {
	t.Helper()
	if execPath == "" {
		execPath = dir + "/runtime-state/execution.json"
	}
	sp := &fakeSpawner{}
	log := &eventLog{}
	fl := &fakeLander{}
	wf := testLane(4, steps...)
	wf.LockInterval = &workflow.LeaseInterval{Enter: enter, Exit: exit}
	d, err := New(Config{
		WI:             WIInfo{ID: "wi_land", CapsuleDir: dir, BundleName: "b"},
		ExecPath:       execPath,
		Workflow:       wf,
		Spawner:        sp,
		Log:            log,
		IntervalLeases: fl,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, sp, log, fl
}

// TestLeaseProjectionIntervalAndRelease: one hold covers the interval, the
// release comes when the plan leaves it, and a step outside the interval
// dispatches no second hold.
func TestLandingLockIntervalAndRelease(t *testing.T) {
	d, sp, _, fl := landingEnv(t, "sync", "merge",
		spec("build"), spec("sync"), spec("merge"), spec("after"))
	sp.script = []fakeEntry{done(""), done(""), done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	holds, releases := fl.counts()
	if holds != 1 || releases < 1 {
		t.Fatalf("holds %d releases %d, want one hold released when the interval ended", holds, releases)
	}
	if got := sp.dispatched(); len(got) != 4 {
		t.Fatalf("dispatches: %v", got)
	}
}

// TestLeaseProjectionQueueWaitAnnounced: waiting for the lease is a queue
// fact, not a rewind — the driver takes the lease before the enter
// step's first dispatch and announces the position.
func TestLandingLockQueueWaitAnnounced(t *testing.T) {
	d, sp, log, fl := landingEnv(t, "sync", "merge", spec("build"), spec("sync"), spec("merge"))
	fl.queued = true
	sp.script = []fakeEntry{done(""), done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !log.has(EvLeaseQueued, "step", "sync") {
		t.Fatalf("no %s fact for the enter step", EvLeaseQueued)
	}
	if !log.has(EvLeaseGranted, "step", "sync") {
		t.Fatalf("no %s fact for the enter step", EvLeaseGranted)
	}
	exec := readExec(t, d.execPath)
	if exec.RewindStats.Count != 0 {
		t.Fatalf("waiting charged %d rewind(s), want none", exec.RewindStats.Count)
	}
}

// TestLandingLossReentersAtEnter: a lost lease releases the group and the
// steps from the declared enter step run again, charged to no rewind
// budget (class landing).
func TestLandingLossReentersAtEnter(t *testing.T) {
	d, sp, _, fl := landingEnv(t, "sync", "publish",
		spec("build"), spec("sync"), spec("merge"), spec("publish"), spec("after"))
	lose := done("")
	lose.act = func() { fl.lose(errors.New("the landing lease was lost")) }
	// the merge dispatch (second in the interval) loses the lease
	sp.script = []fakeEntry{done(""), done(""), lose, done(""), done(""), done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, d.execPath)
	var landing []capsule.RewindRecord
	for _, r := range exec.RewindStats.Rewinds {
		if r.Class == capsule.RewindLockCycle {
			landing = append(landing, r)
		}
	}
	if len(landing) != 1 || landing[0].ToStep != "sync" {
		t.Fatalf("landing rewind = %+v, want one to sync", landing)
	}
	if AgentRewindCount(exec.RewindStats.Rewinds) != 0 {
		t.Fatalf("the landing re-entry was charged to the rewind budget")
	}
	holds, _ := fl.counts()
	if holds < 2 {
		t.Fatalf("holds %d, want the group re-taken after the loss", holds)
	}
	if exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("workflow status %q, want completed", exec.WorkflowStatus)
	}
}

// TestLandingAskReleasesAndReenters: before the owner is asked the lease
// is released; the answer re-enters at the declared enter step instead of
// resuming at the step that asked.
func TestLandingAskReleasesAndReenters(t *testing.T) {
	d, _, _, fl := landingEnv(t, "sync", "publish",
		spec("build"), spec("sync"), spec("merge"), spec("publish"))
	if err := d.transition(TransitionRequest{Kind: TransInit, SpawnN: d.currentSpawnN()}); err != nil {
		t.Fatal(err)
	}
	d.leaseHold = fl
	fl.held = true
	d.releaseLeaseForAsk("merge")
	if _, releases := fl.counts(); releases != 1 {
		t.Fatalf("ask released %d, want 1", releases)
	}
	handled, err := d.ensureLease(context.Background(), "merge")
	if err != nil || !handled {
		t.Fatalf("ensureLease after the ask = %v, %v; want a rewind", handled, err)
	}
	exec := readExec(t, d.execPath)
	if exec.CurrentStep != "sync" {
		t.Fatalf("after the ask the position is %q, want sync", exec.CurrentStep)
	}
	if len(exec.RewindStats.Rewinds) != 1 || exec.RewindStats.Rewinds[0].Class != capsule.RewindLockCycle {
		t.Fatalf("rewind record %+v, want one landing record", exec.RewindStats.Rewinds)
	}
}

// TestLandingOutsideIntervalTakesNone: a lane without a declared interval
// never asks the lander, and a step before the declared enter dispatches
// with no lease held.
func TestLandingOutsideIntervalTakesNone(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{}
	fl := &fakeLander{}
	d, err := New(Config{
		WI: WIInfo{ID: "wi_nolease", CapsuleDir: dir, BundleName: "b"}, ExecPath: dir + "/runtime-state/execution.json",
		Workflow: testLane(4, spec("a"), spec("b")), Spawner: sp, Log: &eventLog{}, IntervalLeases: fl,
	})
	if err != nil {
		t.Fatal(err)
	}
	sp.script = []fakeEntry{done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if holds, _ := fl.counts(); holds != 0 {
		t.Fatalf("a lane without an interval took %d lease(s)", holds)
	}

	d2, sp2, _, fl2 := landingEnv(t, "sync", "merge", spec("build"), spec("sync"), spec("merge"))
	sp2.script = []fakeEntry{done(""), done(""), done("")}
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if holds, _ := fl2.counts(); holds != 1 {
		t.Fatalf("holds %d, want one taken at sync only", holds)
	}
}

// TestLandingExitReleasesBeforeNextStep: the exit step's lease is given
// back before the next (outside) step dispatches.
func TestLandingExitReleasesBeforeNextStep(t *testing.T) {
	d, sp, _, fl := landingEnv(t, "sync", "merge", spec("build"), spec("sync"), spec("merge"), spec("after"))
	after := done("")
	after.act = func() {
		fl.mu.Lock()
		held := fl.held
		fl.mu.Unlock()
		if held {
			t.Errorf("the after step dispatched while the interval lease was still held")
		}
	}
	sp.script = []fakeEntry{done(""), done(""), done(""), after}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, releases := fl.counts(); releases != 1 {
		t.Fatalf("releases %d, want exactly one", releases)
	}
}

// TestLandingGateFailReleases: a verification-style gate FAIL rewinds to its
// on_fail target (outside the interval): the lease is released before that
// step dispatches, and the rewind is charged normally.
func TestLandingGateFailReleases(t *testing.T) {
	merge := spec("merge")
	merge.Gate = true
	merge.OnFail = &OnFail{Target: "build"}
	d, sp, _, fl := landingEnv(t, "sync", "merge", spec("build"), spec("sync"), merge)
	buildAfter := done("")
	buildAfter.act = func() {
		fl.mu.Lock()
		held := fl.held
		fl.mu.Unlock()
		if held {
			t.Errorf("the on_fail target dispatched under the released interval's lease")
		}
	}
	sp.script = []fakeEntry{done(""), done(""), gateFAIL(), buildAfter, done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, d.execPath)
	if AgentRewindCount(exec.RewindStats.Rewinds) != 1 {
		t.Fatalf("rewind count %d, want the FAIL charged once", AgentRewindCount(exec.RewindStats.Rewinds))
	}
}

// TestLandingLaterGateFailChargedAfterLoss: a lost lease re-enters at
// enter through the Q7 path (the dispatch's result is dropped); a LATER
// verification FAIL in the same run still follows on_fail to build and is
// charged (review r3#1: a lost-cycle marker must not leak into the next
// cycle).
func TestLandingLaterGateFailChargedAfterLoss(t *testing.T) {
	ver := spec("verification")
	ver.Gate = true
	ver.OnFail = &OnFail{Target: "build"}
	d, sp, _, fl := landingEnv(t, "sync", "verification", spec("build"), spec("sync"), ver)
	build := done("")
	build.act = func() { fl.loseSticky(errors.New("the landing lock was lost (renewing app)")) }
	held := done("")
	held.hold = true
	failing := fakeEntry{res: &StepResult{Handoff: handoffOf(stepcall.StatusFail, "the landing lock test case failed")}}
	// cycle 1: build (loses), sync (held, stopped), then cycle 2: build,
	// sync, verification FAIL -> build, then verification done.
	reentered := done("")
	reentered.act = func() { fl.clear() }
	sp.script = []fakeEntry{build, held, reentered, failing, done(""), done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, d.execPath)
	if len(exec.RewindStats.Rewinds) != 1 {
		t.Fatalf("rewinds %+v, want only the verification FAIL", exec.RewindStats.Rewinds)
	}
	last := exec.RewindStats.Rewinds[0]
	if last.ToStep != "build" || last.Class != capsule.RewindCharged || last.Gate != "verification" {
		t.Fatalf("rewind %+v, want the verification FAIL charged to build", last)
	}
	if AgentRewindCount(exec.RewindStats.Rewinds) != 1 {
		t.Fatalf("charged rewinds %d, want exactly the verification FAIL", AgentRewindCount(exec.RewindStats.Rewinds))
	}
}

// TestLandingBlockedResumeReentersEnter: a blocked workflow persists the
// re-entry intent; the resume lands it as a rewind to enter, uncharged.
func TestLandingBlockedResumeReentersEnter(t *testing.T) {
	sync := spec("sync")
	merge := spec("merge")
	d, sp, _, _ := landingEnv(t, "sync", "merge", spec("build"), sync, merge)
	sp.script = []fakeEntry{done(""), done(""), incomplete()}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, d.execPath)
	if exec.WorkflowStatus != StatusBlocked || exec.LeaseReentry != "sync" {
		t.Fatalf("blocked=%q marker=%q, want blocked at merge with marker sync", exec.WorkflowStatus, exec.LeaseReentry)
	}
	d2, sp2, _, _ := landingEnvAt(t, d.wi.CapsuleDir, d.execPath, "sync", "merge", spec("build"), sync, merge)
	sp2.script = []fakeEntry{done(""), done("")}
	if err := d2.ResumeCurrent(); err != nil {
		t.Fatalf("ResumeCurrent: %v", err)
	}
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("Run after resume: %v", err)
	}
	exec = readExec(t, d.execPath)
	var landing []capsule.RewindRecord
	for _, r := range exec.RewindStats.Rewinds {
		if r.Class == capsule.RewindLockCycle {
			landing = append(landing, r)
		}
	}
	if len(landing) != 1 || landing[0].ToStep != "sync" {
		t.Fatalf("landing rewind %+v, want one to sync", landing)
	}
	if exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("status %q, want completed after the resume", exec.WorkflowStatus)
	}
}

// TestLandingRenewLossStopsDispatch: a lease lost while a dispatch runs
// stops that dispatch and re-enters at enter, uncharged.
// TestLandingGateFailOutsideIntervalStaysCharged: a review gate's FAIL
// (before the interval) keeps its on_fail target and is charged, even with
// a lost lease recorded and the words in its reason (review r2#2).
func TestLandingGateFailOutsideIntervalStaysCharged(t *testing.T) {
	review := spec("review")
	review.Gate = true
	review.OnFail = &OnFail{Target: "build"}
	d, sp, _, _ := landingEnv(t, "sync", "merge", spec("build"), review, spec("sync"), spec("merge"))
	lost := fakeEntry{res: &StepResult{Handoff: handoffOf(stepcall.StatusFail, "the landing lock was lost (renewing app)")}}
	sp.script = []fakeEntry{done(""), lost, done(""), done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, d.execPath)
	if len(exec.RewindStats.Rewinds) != 1 || exec.RewindStats.Rewinds[0].ToStep != "build" || exec.RewindStats.Rewinds[0].Class != capsule.RewindCharged {
		t.Fatalf("rewinds %+v, want the review FAIL charged to build", exec.RewindStats.Rewinds)
	}
}

// TestLandingVerificationFailReturnsToBuild: a real verification FAIL
// (inside the interval, no lost lease) keeps on_fail build and is charged,
// even when its reason mentions the lock (review r2#2).
func TestLandingVerificationFailReturnsToBuild(t *testing.T) {
	ver := spec("verification")
	ver.Gate = true
	ver.OnFail = &OnFail{Target: "build"}
	d, sp, _, _ := landingEnv(t, "sync", "merge", spec("build"), spec("sync"), ver, spec("merge"))
	lost := fakeEntry{res: &StepResult{Handoff: handoffOf(stepcall.StatusFail, "the landing lock test case failed")}}
	sp.script = []fakeEntry{done(""), done(""), lost, done(""), done(""), done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, d.execPath)
	if len(exec.RewindStats.Rewinds) != 1 || exec.RewindStats.Rewinds[0].ToStep != "build" || exec.RewindStats.Rewinds[0].Class != capsule.RewindCharged {
		t.Fatalf("rewinds %+v, want the verification FAIL charged to build", exec.RewindStats.Rewinds)
	}
}

// TestLandingCancelReleases: a cancelled run releases the interval lease.
func TestLandingCancelReleases(t *testing.T) {
	d, sp, _, fl := landingEnv(t, "sync", "merge", spec("build"), spec("sync"), spec("merge"))
	sp.script = []fakeEntry{done(""), {hold: true}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitUntil(t, "the interval lease held", func() bool {
		fl.mu.Lock()
		defer fl.mu.Unlock()
		return fl.held
	})
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the cancel")
	}
	fl.mu.Lock()
	held := fl.held
	fl.mu.Unlock()
	if held {
		t.Fatal("the interval lease stayed held after the cancel")
	}
	if _, releases := fl.counts(); releases != 1 {
		t.Fatalf("releases %d, want one", releases)
	}
}

// TestLandingSkippedExitReleases: skipping the exit step leaves the
// interval and releases before the next step dispatches.
func TestLandingSkippedExitReleases(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{}
	log := &eventLog{}
	fl := &fakeLander{}
	merge := spec("merge")
	merge.Skippable = true
	wf := testLane(4, spec("build"), spec("sync"), merge, spec("after")).WithSkipped([]string{"merge"})
	wf.LockInterval = &workflow.LeaseInterval{Enter: "sync", Exit: "merge"}
	d, err := New(Config{
		WI: WIInfo{ID: "wi_skip", CapsuleDir: dir, BundleName: "b"}, ExecPath: dir + "/runtime-state/execution.json",
		Workflow: wf, Spawner: sp, Log: log, IntervalLeases: fl,
	})
	if err != nil {
		t.Fatal(err)
	}
	after := done("")
	after.act = func() {
		fl.mu.Lock()
		held := fl.held
		fl.mu.Unlock()
		if held {
			t.Errorf("the after step dispatched under the skipped interval's lease")
		}
	}
	sp.script = []fakeEntry{done(""), done(""), after}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, releases := fl.counts(); releases != 1 {
		t.Fatalf("releases %d, want one", releases)
	}
}

// TestLeaseQueueFactReachesSupervisor: waiting for the lease sends the
// supervisor a StepQueued fact, never a stall.
func TestLeaseQueueFactReachesSupervisor(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{}
	log := &eventLog{}
	fl := &fakeLander{queued: true}
	link := supervise.NewLink("wi_q")
	wf := testLane(4, spec("sync"), spec("merge"))
	wf.LockInterval = &workflow.LeaseInterval{Enter: "sync", Exit: "merge"}
	d, err := New(Config{
		WI: WIInfo{ID: "wi_q", CapsuleDir: dir, BundleName: "b"}, ExecPath: dir + "/runtime-state/execution.json",
		Workflow: wf, Spawner: sp, Log: log, IntervalLeases: fl, Link: link,
	})
	if err != nil {
		t.Fatal(err)
	}
	sp.script = []fakeEntry{done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	queued := false
	for _, f := range link.Facts.Drain() {
		if q, ok := f.(supervise.StepQueued); ok && q.Step == "sync" {
			queued = true
		}
	}
	if !queued {
		t.Fatal("no StepQueued fact reached the supervisor for the waiting enter step")
	}
}

func TestLandingRenewLossStopsDispatch(t *testing.T) {
	merge := spec("merge")
	d, sp, _, fl := landingEnv(t, "sync", "merge", spec("build"), spec("sync"), merge)
	build := done("")
	build.act = func() { fl.loseSticky(errors.New("the landing lock was lost (renewing app)")) }
	held := done("")
	held.hold = true
	reentered := done("")
	reentered.act = func() { fl.clear() }
	sp.script = []fakeEntry{build, held, reentered, done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, d.execPath)
	if AgentRewindCount(exec.RewindStats.Rewinds) != 0 {
		t.Fatal("the lost lease was charged to the rewind budget")
	}
	// the loss happened on the enter step itself: its re-entry needs no
	// rewind record, but it must take the lease again (holds == 2)
	if holds, _ := fl.counts(); holds != 2 {
		t.Fatalf("holds %d, want the original and the re-entry hold", holds)
	}
}

// TestLandingGateAskResumesInPlace: owner ruling 2026-11-24 (1A) — a gate's
// ask resumes that gate; no re-entry marker is written.
func TestLandingGateAskResumesInPlace(t *testing.T) {
	merge := spec("merge")
	merge.Gate = true
	d, _, _, fl := landingEnv(t, "sync", "merge", spec("sync"), merge)
	if err := d.transition(TransitionRequest{Kind: TransInit, SpawnN: d.currentSpawnN()}); err != nil {
		t.Fatal(err)
	}
	d.leaseHold = fl
	fl.held = true
	d.releaseLeaseForAsk("merge")
	exec := readExec(t, d.execPath)
	if exec.LeaseReentry != "" {
		t.Fatalf("a gate ask wrote the re-entry marker %q", exec.LeaseReentry)
	}
	if _, releases := fl.counts(); releases != 1 {
		t.Fatalf("releases %d, want the gate ask to release once", releases)
	}
}

// TestLandingEnterBeforeFirstDispatch: the lease is taken before the enter
// step's first dispatch, never for a step before it.
func TestLandingEnterBeforeFirstDispatch(t *testing.T) {
	d, sp, _, fl := landingEnv(t, "sync", "merge", spec("build"), spec("sync"), spec("merge"))
	at := map[string]int{}
	mark := func(step string) func() {
		return func() {
			fl.mu.Lock()
			held := fl.held
			fl.mu.Unlock()
			if held {
				at[step]++
			}
		}
	}
	b, y, m := done(""), done(""), done("")
	b.act = mark("build")
	y.act = mark("sync")
	m.act = mark("merge")
	sp.script = []fakeEntry{b, y, m}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if at["build"] != 0 || at["sync"] != 1 || at["merge"] != 1 {
		t.Fatalf("held at dispatch: %v, want build 0, sync 1, merge 1", at)
	}
}

// TestLandingLossDuringEnterHoldsAgain: the enter step itself loses the
// lease while running; its re-entry dispatch must HOLD the lease before it
// runs (review r4#1): the fix is not to skip Hold when the plan already
// stands on enter.
func TestLandingLossDuringEnterHoldsAgain(t *testing.T) {
	d, sp, _, fl := landingEnv(t, "sync", "verification", spec("build"), spec("sync"), spec("verification"))
	build := done("")
	build.act = func() { fl.loseSticky(errors.New("the landing lock was lost in sync")) }
	held := done("")
	held.hold = true
	reentered := done("")
	reentered.act = func() {
		fl.mu.Lock()
		heldNow := fl.held
		fl.mu.Unlock()
		if !heldNow {
			t.Errorf("the re-entered enter step dispatched without holding the lease")
		}
		fl.clear()
	}
	sp.script = []fakeEntry{build, held, reentered, done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if holds, _ := fl.counts(); holds != 2 {
		t.Fatalf("holds %d, want the initial and the re-entry hold", holds)
	}
}

// TestLandingAskOnEnterHoldsAgain: an ask on the enter step (a non-gate
// step) releases and re-enters at that same step; the re-entry takes the
// lease before dispatching (review r4#1).
func TestLandingAskOnEnterHoldsAgain(t *testing.T) {
	d, _, _, fl := landingEnv(t, "sync", "verification", spec("sync"), spec("verification"))
	if err := d.transition(TransitionRequest{Kind: TransInit, SpawnN: d.currentSpawnN()}); err != nil {
		t.Fatal(err)
	}
	d.leaseHold = fl
	fl.held = true
	d.releaseLeaseForAsk("sync")
	if _, releases := fl.counts(); releases != 1 {
		t.Fatalf("releases %d, want the ask to release once", releases)
	}
	handled, err := d.ensureLease(context.Background(), "sync")
	if err != nil || handled {
		t.Fatalf("ensureLease after the ask = %v, %v; want the enter re-entry to hold, not rewind", handled, err)
	}
	fl.mu.Lock()
	held := fl.held
	fl.mu.Unlock()
	if !held {
		t.Fatal("the re-entered enter step did not hold the lease")
	}
	if holds, _ := fl.counts(); holds != 1 {
		t.Fatalf("holds %d, want the re-entry's hold", holds)
	}
}
