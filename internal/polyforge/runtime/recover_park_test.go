package runtime

// a recovery whose old driver Run outlives the handle's
// bounded teardown parks the WI instead of starting a second writer, emits
// an owner-visible event, and re-enters recovery by itself once that Run
// exits. The wait is a goroutine, never the WI's actor.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// parkedRunHandle is a bridge-shaped handle whose Run ignores the teardown
// bound: Terminate reports the timeout while Exit/Done stay open until the
// test releases it.
type parkedRunHandle struct {
	mu        sync.Mutex
	released  bool
	exit      chan struct{}
	termCalls int
}

func newParkedRunHandle() *parkedRunHandle {
	return &parkedRunHandle{exit: make(chan struct{})}
}

func (h *parkedRunHandle) PID() int          { return 0 } // the bridge shape: no child pid
func (h *parkedRunHandle) StartedAt() string { return "2026-10-27T00:00:00Z" }
func (h *parkedRunHandle) Alive() bool       { return !h.Done() }
func (h *parkedRunHandle) Exit() <-chan struct{} {
	return h.exit
}
func (h *parkedRunHandle) Done() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.released
}
func (h *parkedRunHandle) Terminate(time.Duration) error {
	h.mu.Lock()
	h.termCalls++
	h.mu.Unlock()
	return fmt.Errorf("driver: did not exit within the teardown bound")
}
func (h *parkedRunHandle) release() {
	h.mu.Lock()
	if !h.released {
		h.released = true
		close(h.exit)
	}
	h.mu.Unlock()
}

func TestRecoverParksOnLiveRunAndRelaunchesWhenItExits(t *testing.T) {
	origBound := cleanupBound
	cleanupBound = 50 * time.Millisecond
	t.Cleanup(func() { cleanupBound = origBound })

	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_w77_park"
	j := seedJob(r, wi)
	old := newParkedRunHandle()
	j.mu.Lock()
	j.agent = old
	j.agentPID = 0
	j.mu.Unlock()
	t.Cleanup(old.release)

	var mu sync.Mutex
	starts, parkedEvents := 0, 0
	r.cfg.EventTap = func(_ string, kind string, _ map[string]any) {
		if kind != EventRecoveryParked {
			return
		}
		mu.Lock()
		parkedEvents++
		mu.Unlock()
	}
	starter := r.cfg.AgentStarter
	r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		mu.Lock()
		starts++
		mu.Unlock()
		return starter(ctx, in)
	}

	done := make(chan struct{})
	go func() {
		r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
		close(done)
	}()
	// The whole TerminateAgentHandle wait (2 x cleanupBound) passed while the
	// old Run stayed alive: recovery must return parked, not block the actor.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery blocked the actor on a live old Run instead of parking")
	}

	mu.Lock()
	n, events := starts, parkedEvents
	mu.Unlock()
	if n != 0 {
		t.Fatalf("the engine started %d time(s) next to the live old Run (double writer)", n)
	}
	if events != 1 {
		t.Fatalf("parked events: %d, want 1 (the park must be owner-visible)", events)
	}
	if st, _ := j.snapshot(); st != StateRecovering {
		t.Fatalf("parked state %q, want %q", st, StateRecovering)
	}
	j.mu.Lock()
	held := j.agent == old
	j.mu.Unlock()
	if !held {
		t.Fatal("the park dropped the live handle: recovery can no longer tell the job has a writer")
	}

	// The old Run exits: recovery re-enters on its own and relaunches.
	old.release()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n = starts
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n == 0 {
		t.Fatal("recovery never re-entered after the old Run exited (the park has no exit)")
	}
	// The starter answers before the actor's recovered() transition: wait for
	// the relaunch to settle, not for the intermediate RECOVERING snapshot.
	for time.Now().Before(deadline) {
		if st, _ := j.snapshot(); st == StateRunning {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, _ := j.snapshot()
	t.Fatalf("state after the relaunch %q, want %q", st, StateRunning)
}

// TestRecoveryParkDoesNotResurrectAClosedJob pins the other side of the
// park: a WI closed (Stop → stopOp: RECOVERING→SEALING) while its recovery
// waits on the old Run must NOT be resumed when that Run exits. The close's
// own killAgentTree fails again (the Run is still alive), so the close parks
// on the same handle (parkClose) and j.agent still names it — which is why
// the recovery re-entry needs the table and state checks, not just
// j.agent == h. Once the Run exits the close finishes, the recovery stands
// down.
func TestRecoveryParkDoesNotResurrectAClosedJob(t *testing.T) {
	origBound := cleanupBound
	cleanupBound = 50 * time.Millisecond
	t.Cleanup(func() { cleanupBound = origBound })

	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_w77_park_closed"
	j := seedJob(r, wi)
	old := newParkedRunHandle()
	j.mu.Lock()
	j.agent = old
	j.agentPID = 0
	j.mu.Unlock()
	t.Cleanup(old.release)

	var mu sync.Mutex
	starts, parkedEvents := 0, 0
	r.cfg.EventTap = func(_ string, kind string, _ map[string]any) {
		if kind != EventRecoveryParked {
			return
		}
		mu.Lock()
		parkedEvents++
		mu.Unlock()
	}
	starter := r.cfg.AgentStarter
	r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		mu.Lock()
		starts++
		mu.Unlock()
		return starter(ctx, in)
	}

	done := make(chan struct{})
	go func() {
		r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
		close(done)
	}()
	// Wait for the park (recovery returned, event emitted, handle kept).
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		events := parkedEvents
		mu.Unlock()
		if events == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recovery never parked on the live old Run")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery blocked the actor on a live old Run instead of parking")
	}
	if st, _ := j.snapshot(); st != StateRecovering {
		t.Fatalf("parked state %q, want %q", st, StateRecovering)
	}

	// The owner closes the WI while it is parked. stopOp's killAgentTree
	// fails again on the still-live Run, so the close parks in SEALING with
	// j.agent still h.
	if err := r.stopOp(context.Background(), j, protocol.FinishWrapped, false, ""); err == nil || !j.closeParked() {
		t.Fatalf("a close next to the live Run must park, got err=%v parked=%v", err, j.closeParked())
	}
	if st, _ := j.snapshot(); st != StateSealing {
		t.Fatalf("parked close state %q, want %q", st, StateSealing)
	}

	// Hold the actor so both parks' re-entries (the recovery's and the
	// close's) are still queued when the old Run exits: their queue
	// positions are then observable, and the sentinel below is ordered after
	// them (no sleep-based race).
	blocked := make(chan struct{})
	unblock := make(chan struct{})
	r.post(wi, func() {
		close(blocked)
		<-unblock
	})
	<-blocked
	old.release()
	deadline = time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		mb := r.boxes[wi]
		queued := mb != nil && len(mb.q) >= 2
		r.mu.Unlock()
		if queued {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the parks never queued their re-entries after the old Run exited")
		}
		time.Sleep(time.Millisecond)
	}
	close(unblock)
	settled := make(chan struct{})
	r.post(wi, func() { close(settled) })
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("the queued re-entry never settled")
	}

	mu.Lock()
	n := starts
	mu.Unlock()
	if n != 0 {
		t.Fatalf("the engine started %d time(s) for a closed WI after the old Run exited (resurrection)", n)
	}
	if st, _ := j.snapshot(); st != StateDone {
		t.Fatalf("state after the old Run exited %q, want %q (the park overwrote the close)", st, StateDone)
	}
	if r.jobOf(wi) != nil {
		t.Fatal("the dropped job came back after the park re-entered")
	}
}
