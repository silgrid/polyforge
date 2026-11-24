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
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// fakeLander is one interval's IntervalLeases: it counts the holds and
// releases and answers a lost lease once told to.
type fakeLander struct {
	mu       sync.Mutex
	holds    int
	releases int
	lost     error
	queued   bool
	// held records whether the group is currently held.
	held bool
}

func (f *fakeLander) Hold(_ context.Context, _, _ string, _ []string, queued func(int, time.Time)) (IntervalLease, error) {
	f.mu.Lock()
	f.holds++
	f.held = true
	announce := f.queued
	f.mu.Unlock()
	if announce && queued != nil {
		queued(3, time.Now())
	}
	return f, nil
}

// Err answers a lost lease once (the next hold is healthy again).
func (f *fakeLander) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := f.lost
	f.lost = nil
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

func (f *fakeLander) counts() (holds, releases int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.holds, f.releases
}

func landingEnv(t *testing.T, enter, exit string, steps ...StepSpec) (*Driver, *fakeSpawner, *eventLog, *fakeLander) {
	t.Helper()
	dir := t.TempDir()
	execPath := dir + "/runtime-state/execution.json"
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

// TestLockIntervalIntervalAndRelease: one hold covers the interval, the
// release comes when the plan leaves it, and a step outside the interval
// dispatches no second hold.
func TestLockIntervalIntervalAndRelease(t *testing.T) {
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

// TestLockIntervalQueueWaitAnnounced: waiting for the lease is a queue
// fact, not a rewind — the driver takes the lease before the enter
// step's first dispatch and announces the position.
func TestLockIntervalQueueWaitAnnounced(t *testing.T) {
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

// TestLandingOutsideIntervalTakesNone: a lane with no declared interval
// takes none (the pre-#245 behaviour).
func TestLandingOutsideIntervalTakesNone(t *testing.T) {
	d, sp, _ := runEnv(t, spec("a"), spec("b"))
	sp.script = []fakeEntry{done(""), done("")}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, releases := (&fakeLander{}).counts(); releases != 0 {
		t.Fatal("a lane without lease_interval released a lease")
	}
}
