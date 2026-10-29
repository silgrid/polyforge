package runtime

// A parked close or release owns its job until the old driver Run exits:
// no other operation of the work item may act on it meanwhile (a stall
// report must not start a recovery, a reconcile must not replay the close,
// a resume must not flip it to RUNNING), and of two parks on one handle
// only the standing one finishes.

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// countEngineStarts wraps r's engine starter with a counter.
func countEngineStarts(r *Runtime) *atomic.Int32 {
	var n atomic.Int32
	starter := r.cfg.AgentStarter
	r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		n.Add(1)
		return starter(ctx, in)
	}
	return &n
}

// drainParkWaiters holds wi's actor while the old Run exits, waits until
// at least n re-entries are queued behind it, then lets them run and waits
// for every one of them to settle — the order the waiters posted in does
// not matter to the caller's assertions.
func drainParkWaiters(t *testing.T, r *Runtime, wi string, release func(), n int) {
	t.Helper()
	blocked, unblock := make(chan struct{}), make(chan struct{})
	r.post(wi, func() {
		close(blocked)
		<-unblock
	})
	<-blocked
	release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		mb := r.boxes[wi]
		queued := mb != nil && len(mb.q) >= n
		r.mu.Unlock()
		if queued {
			break
		}
		if time.Now().After(deadline) {
			close(unblock)
			t.Fatalf("fewer than %d park re-entries queued after the old Run exited", n)
		}
		time.Sleep(time.Millisecond)
	}
	close(unblock)
	settled := make(chan struct{})
	r.post(wi, func() { close(settled) })
	select {
	case <-settled:
	case <-time.After(10 * time.Second):
		t.Fatal("the queued park re-entries never settled")
	}
}

// TestParkedReleaseOwnsTheJobAgainstRecovery: a release parked on a
// RUNNING job (OBSuperseded while the Run ignores its teardown) keeps the
// job RUNNING; a stall/death report for it must not start a recovery
// (RECOVERING, a recovery park, a relaunch) next to the parked release.
func TestParkedReleaseOwnsTheJobAgainstRecovery(t *testing.T) {
	k := newReleaseParkRig(t, "wi_park_guard_recovery")
	starts := countEngineStarts(k.r)

	k.r.handleOB(context.Background(), protocol.OBMsg{Kind: protocol.OBSuperseded, WorkItem: k.j.wi, SessionFP: k.j.sessionFingerprint})
	if !k.j.closeParked() {
		t.Fatal("precondition: the release did not park on the live Run")
	}
	k.r.handleOB(context.Background(), recoveryMsg(k.j, protocol.RecoverDeath))
	if st, _ := k.j.snapshot(); st != StateRunning {
		t.Fatalf("a recovery report over the parked release moved the job to %q", st)
	}
	if n := k.event(EventRecoveryParked); n != 0 {
		t.Fatalf("a recovery ran next to the parked release (%d recovery_parked events)", n)
	}

	drainParkWaiters(t, k.r, k.j.wi, k.old.release, 1)
	if n := k.event("session_superseded"); n != 1 || k.r.jobOf(k.j.wi) != nil {
		t.Fatalf("the parked release must finish exactly once: %d session_superseded, job present=%v", n, k.r.jobOf(k.j.wi) != nil)
	}
	if n := starts.Load(); n != 0 {
		t.Fatalf("the engine started %d time(s) for a released work item", n)
	}
}

// TestParkedCloseOwnsTheJobAgainstReconcile: a reconcile (a retry timer,
// a restart pass) of a job whose close is parked must leave it to the
// park's waiter — no verify, no replayed close, no second park.
func TestParkedCloseOwnsTheJobAgainstReconcile(t *testing.T) {
	k := newCloseParkRig(t, "wi_park_guard_reconcile")

	assertParked409(t, k.r.Stop(context.Background(), k.j.wi, protocol.FinishWrapped), "the close of a live Run")
	k.hub.mu.Lock()
	verifies := k.hub.verifies[k.j.wi]
	k.hub.mu.Unlock()

	underLock(k.r, k.j.wi, func() { k.r.reconcile(k.j.wi) })
	k.hub.mu.Lock()
	after := k.hub.verifies[k.j.wi]
	k.hub.mu.Unlock()
	if after != verifies {
		t.Fatalf("the reconcile verified the parked job's session (%d -> %d verifies)", verifies, after)
	}
	if ev := k.parkedEvents(); len(ev) != 1 {
		t.Fatalf("the reconcile replayed the parked close: %d close_parked events", len(ev))
	}
	k.assertNoCapsuleWrite(t, "a reconcile over the parked close")

	k.old.release()
	waitRT(t, k.toOB, protocol.FinishWrapped)
	if n := k.hub.endCount(); n != 1 || k.hub.lastEnd().Status != protocol.FinishWrapped {
		t.Fatalf("the parked close must finish exactly once: %d SessionEnd, last %+v", n, k.hub.lastEnd())
	}
}

// TestReplacedParkWaiterStandsDown: a later park on the same handle (a
// release replacing a parked close) supersedes the earlier one: once the
// Run exits only the standing park finishes, whichever waiter runs first.
func TestReplacedParkWaiterStandsDown(t *testing.T) {
	k := newCloseParkRig(t, "wi_park_guard_replaced")
	var first, second atomic.Int32
	underLock(k.r, k.j.wi, func() {
		_ = k.r.parkClose(k.j, "close", protocol.FinishWrapped, func() { first.Add(1) })
		_ = k.r.parkClose(k.j, parkKindRelease, "session_released", func() { second.Add(1) })
	})

	drainParkWaiters(t, k.r, k.j.wi, k.old.release, 2)
	if a, b := first.Load(), second.Load(); a != 0 || b != 1 {
		t.Fatalf("finishes after the Run exited: replaced park %d (want 0), standing park %d (want 1)", a, b)
	}
	if k.j.closeParked() {
		t.Fatal("the finished park is still marked")
	}
}

// TestResumeRecordRefusesAParkedJob: resumeRecord (an operator resume, a
// restart pass, a parked recovery's re-entry) over a job whose close or
// release is parked answers the park like every other door — without
// first moving the job to RUNNING.
func TestResumeRecordRefusesAParkedJob(t *testing.T) {
	k := newCloseParkRig(t, "wi_park_guard_resume")
	starts := countEngineStarts(k.r)
	k.j.mu.Lock()
	k.j.state = StateRecovering // the shape a parked recovery leaves
	k.j.mu.Unlock()
	underLock(k.r, k.j.wi, func() {
		_ = k.r.parkClose(k.j, parkKindRelease, "session_released", func() {})
	})

	var out string
	var err error
	underLock(k.r, k.j.wi, func() { out, err = k.r.resumeRecord(k.j, causeOperatorResume, "test resume") })
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "release of this work item is parked") {
		t.Fatalf("resumeRecord over a parked release answered (%q, %v), want the parked DAEMON-409", out, err)
	}
	if st, _ := k.j.snapshot(); st != StateRecovering {
		t.Fatalf("resumeRecord moved the parked job to %q", st)
	}
	if n := starts.Load(); n != 0 {
		t.Fatalf("the engine started %d time(s) next to the parked release", n)
	}
}
