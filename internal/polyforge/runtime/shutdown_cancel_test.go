package runtime

// shutdown_cancel_test.go — the shutdown cancels the actors' context
// (Close): it waits on no actor, and from then on no agent starts — an
// agent start under way is torn down as it binds, a claim is deferred
// with its journal entry kept, a recovery stays RECOVERING.

import (
	"context"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// A recovery whose commit-dirty push hangs does not hold the shutdown,
// and once the push returns it relaunches nothing: the job stays
// RECOVERING (persisted so) for the next daemon start.
func TestShutdownHungRecoveryPushRelaunchesNothing(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	t.Cleanup(r.Close)
	const a = "wi_sl_rec"
	startRunning(t, r, a)
	release := stallPushes(t, r, a)
	ja := r.jobOf(a)
	spawns := sp.callCount()
	recovered := make(chan struct{})
	go func() { r.recoverOp(context.Background(), ja, "agent exited", ""); close(recovered) }()
	waitBusy(t, r, a)

	within(t, promptly, "the shutdown during the hung recovery push", r.Close)
	release()
	select {
	case <-recovered:
	case <-time.After(20 * time.Second):
		t.Fatal("the recovery never returned")
	}
	if n := sp.callCount() - spawns; n != 0 {
		t.Fatalf("the recovery relaunched %d agent(s) behind the latch", n)
	}
	if ji, _ := r.Info(a); ji.State != StateRecovering || ji.Agent != nil {
		t.Fatalf("job after the latched recovery = state %s agent %v, want RECOVERING with no agent", ji.State, ji.Agent != nil)
	}
	recs := loadRecords(t, r)
	if len(recs) != 1 || recs[0].State != string(StateRecovering) {
		t.Fatalf("persisted records = %+v, want the recovering record", recs)
	}
}

// An agent start under way when the shutdown comes is torn down by its
// binder, and the claim is deferred like any the shutdown caught: not
// RUNNING, journal entry kept, no RTStarted; a later start defers
// without claiming.
func TestShutdownLateBoundClaimIsDeferred(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const a = "wi_sl_late_claim"
	inner := r.cfg.AgentStarter
	entered := make(chan struct{})
	proceed := make(chan struct{})
	var h AgentHandle
	r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		close(entered)
		<-proceed
		var err error
		h, err = inner(ctx, in)
		return h, err
	}
	started := make(chan error, 1)
	go func() { started <- r.Start(context.Background(), a, StartOpts{}) }()
	<-entered
	within(t, promptly, "the shutdown during an agent start", r.Close)
	close(proceed)
	if err := recvErr(t, started, "the late Start"); !IsClaimDeferred(err) {
		t.Fatalf("late-bound Start = %v, want the deferral", err)
	}
	if h == nil || h.Alive() {
		t.Fatal("the late-bound agent was not torn down")
	}
	if r.jobOf(a) != nil {
		t.Fatal("the deferred claim left its job")
	}
	if journaled(r, a) == nil {
		t.Fatal("the deferred claim dropped its journal entry (the next start must replay it)")
	}
	for {
		m, ok := toOB.Pop()
		if !ok {
			break
		}
		if m.Kind == protocol.RTStarted {
			t.Fatalf("a late-bound claim announced %+v", m)
		}
	}
	claims := hub.claimCount()
	if err := r.Start(context.Background(), "wi_sl_after", StartOpts{}); !IsClaimDeferred(err) {
		t.Fatalf("a Start after the shutdown = %v, want the deferral", err)
	}
	if hub.claimCount() != claims {
		t.Fatal("a Start after the shutdown claimed")
	}
}

// A recovery relaunch under way when the shutdown comes is torn down as
// it binds: the job stays RECOVERING with no agent and nothing is
// announced.
func TestShutdownLateBoundRecoveryStaysRecovering(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const a = "wi_sl_late_rec"
	startRunning(t, r, a)
	for {
		if _, ok := toOB.Pop(); !ok {
			break
		}
	}
	inner := r.cfg.AgentStarter
	entered := make(chan struct{})
	proceed := make(chan struct{})
	var h AgentHandle
	r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		close(entered)
		<-proceed
		var err error
		h, err = inner(ctx, in)
		return h, err
	}
	done := make(chan struct{})
	go func() { r.recoverOp(context.Background(), r.jobOf(a), "agent exited", ""); close(done) }()
	<-entered
	within(t, promptly, "the shutdown during a relaunch", r.Close)
	close(proceed)
	<-done
	if h == nil || h.Alive() {
		t.Fatal("the late-bound relaunch was not torn down")
	}
	if ji, _ := r.Info(a); ji.State != StateRecovering || ji.Agent != nil {
		t.Fatalf("job after the late relaunch = %s agent %v, want RECOVERING with no agent", ji.State, ji.Agent != nil)
	}
	if m, ok := toOB.Pop(); ok {
		t.Fatalf("a late-bound relaunch announced %+v", m)
	}
}
