package runtime

// State-machine regressions: every legal edge, every illegal edge refused,
// and the message-loop fencing rules (stale attempt-id dropped;
// same-attempt WINeedsRecovery dropped while SEALING; duplicate death
// signals dropped while RECOVERING).

import (
	"context"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestStateMachineLegalEdges(t *testing.T) {
	// RUNNING → SEALING → DONE (stop path)
	j := &job{wi: "wi_sm1", state: StateRunning}
	if err := j.transition(StateSealing); err != nil {
		t.Fatalf("running→sealing: %v", err)
	}
	if err := j.transition(StateDone); err != nil {
		t.Fatalf("sealing→done: %v", err)
	}
	// RUNNING → RECOVERING → RUNNING (recovery succeeded)
	j = &job{wi: "wi_sm1", state: StateRunning}
	if err := j.transition(StateRecovering); err != nil {
		t.Fatalf("running→recovering: %v", err)
	}
	if err := j.transition(StateRunning); err != nil {
		t.Fatalf("recovering→running: %v", err)
	}
	// RUNNING → RECOVERING → DONE (bounded fail)
	j = &job{wi: "wi_sm1", state: StateRunning}
	if err := j.transition(StateRecovering); err != nil {
		t.Fatalf("running→recovering: %v", err)
	}
	if err := j.transition(StateDone); err != nil {
		t.Fatalf("recovering→done: %v", err)
	}
	// Finding 2 (v1.5 review): the operator-pause retention edges —
	// RUNNING → PAUSED (pause close), PAUSED → RUNNING (attach/resume),
	// PAUSED → DONE (later close of the retained record).
	j = &job{wi: "wi_sm1", state: StateRunning}
	if err := j.transition(StatePaused); err != nil {
		t.Fatalf("running→paused: %v", err)
	}
	if err := j.transition(StateRunning); err != nil {
		t.Fatalf("paused→running: %v", err)
	}
	if err := j.transition(StatePaused); err != nil {
		t.Fatalf("running→paused (2): %v", err)
	}
	if err := j.transition(StateDone); err != nil {
		t.Fatalf("paused→done: %v", err)
	}
}

func TestStateMachineIllegalEdges(t *testing.T) {
	cases := []struct {
		from, to JobState
	}{
		{StateRunning, StateRunning},
		{StateRunning, StateDone},    // stop must pass through SEALING
		{StateSealing, StateRunning}, // a seal is not reversible
		{StateSealing, StateSealing},
		{StateSealing, StateRecovering}, // §三: SEALING drops recovery
		{StateSealing, StatePaused},     // a seal is not a pause (Finding 2)
		// NOTE: RECOVERING → SEALING is a LEGAL edge since the v2
		// terminal-close wiring (the #99 finale): a recovery that finds
		// the driver's workflow already terminal translates directly
		// into the sealing close (stopOp from RECOVERING) instead of
		// relaunching into the recovery ledger. It was illegal before that
		// wiring existed.
		{StateRecovering, StateRecovering},
		// Finding 2: the retained paused record cannot re-pause, seal or
		// recover directly — a resume goes through PAUSED → RUNNING, a
		// later close goes through PAUSED → DONE.
		{StatePaused, StateSealing},
		{StatePaused, StateRecovering},
		{StatePaused, StatePaused},
		{StateDone, StateRunning},
		{StateDone, StateSealing},
		{StateDone, StateRecovering},
		{StateDone, StatePaused},
		{StateDone, StateDone},
	}
	for _, c := range cases {
		j := &job{wi: "wi_sm2", state: c.from}
		if err := j.transition(c.to); err == nil {
			t.Fatalf("illegal transition %s → %s must be refused", c.from, c.to)
		}
		if j.state != c.from {
			t.Fatalf("refused transition mutated the state: %s → %s", c.from, j.state)
		}
	}
}

func TestStaleSpawnNDropped(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, fromOB := newTestRuntime(t, hub, sp)
	wi := "wi_fence"
	j := seedJob(r, wi)
	old := installOldAgent(t, sp, j)
	defer old.Terminate(2e9)
	baseline := sp.callCount() // the install itself is one derivation

	// stale counter: Observe still watching a derivation we replaced
	r.handleOB(context.Background(), protocol.OBMsg{
		Kind: protocol.OBNeedsRecovery, SpawnN: 99,
		WorkItem: wi, Reason: protocol.RecoverDeath,
	})
	state, _ := j.snapshot()
	if state != StateRunning {
		t.Fatalf("stale message must not move the state, got %s", state)
	}
	if sp.callCount() != baseline {
		t.Fatal("stale message must not trigger a re-derivation")
	}
	assertNoRT(t, toOB)

	_ = fromOB
}

func TestSealingDropsSameAttemptNeedsRecovery(t *testing.T) {
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_sealdrop"
	j := seedJob(r, wi)
	if err := j.transition(StateSealing); err != nil {
		t.Fatal(err)
	}
	// a duplicate death signal for the very handle being sealed
	r.handleOB(context.Background(), protocol.OBMsg{
		Kind: protocol.OBNeedsRecovery, SpawnN: j.spawnN, WorkItem: wi, Reason: protocol.RecoverDeath,
	})
	if st, _ := j.snapshot(); st != StateSealing {
		t.Fatalf("SEALING must drop same-attempt WINeedsRecovery, got %s", st)
	}
	if sp.callCount() != 0 {
		t.Fatal("SEALING must not re-derive")
	}
	assertNoRT(t, toOB)
}

func TestRecoveringDropsDuplicateDeathSignal(t *testing.T) {
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_dup"
	j := seedJob(r, wi)
	if err := j.transition(StateRecovering); err != nil {
		t.Fatal(err)
	}
	r.handleOB(context.Background(), protocol.OBMsg{
		Kind: protocol.OBNeedsRecovery, SpawnN: j.spawnN, WorkItem: wi, Reason: protocol.RecoverDeath,
	})
	if st, _ := j.snapshot(); st != StateRecovering {
		t.Fatalf("RECOVERING must drop duplicate signals, got %s", st)
	}
	if sp.callCount() != 0 {
		t.Fatal("RECOVERING must not start a second cascade")
	}
	assertNoRT(t, toOB)
}

// TestRunLoopDrivesRecovery proves the loop wiring end-to-end: a WINeedsRecovery
// fed through the fromOB channel reaches the recovery engine and Observe
// hears RTRecovered.
func TestRunLoopDrivesRecovery(t *testing.T) {
	sp := &scriptedSpawner{}
	r, toOB, fromOB := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_loop"
	j := seedJob(r, wi)
	writeTranscript(t, j)
	installOldAgent(t, sp, j)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = r.Run(ctx)
		close(done)
	}()
	fromOB.Push(protocol.OBMsg{
		Kind: protocol.OBNeedsRecovery, SpawnN: j.spawnN, WorkItem: wi, Reason: protocol.RecoverDeath,
	})
	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTRecovered {
		t.Fatalf("expected RTRecovered, got %+v", msg)
	}
	// tidy: tear the recovered sleeper down too
	if j2 := r.jobOf(wi); j2 != nil {
		j2.mu.Lock()
		h := j2.agent
		j2.mu.Unlock()
		if h != nil {
			_ = h.Terminate(2e9)
		}
	}
	cancel()
	select {
	case <-done:
	case <-timeAfter():
		t.Fatal("Run did not exit on ctx cancel")
	}
}
