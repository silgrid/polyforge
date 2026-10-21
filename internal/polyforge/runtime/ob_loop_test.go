package runtime

// ob_loop_test.go — the OB message loop hands each recovery to its own
// goroutine under the work item's lock: one work item's hung recovery
// push does not delay another's recovery, a duplicate death signal of the
// recovering derivation is dropped once the first recovery relaunched,
// and a death signal that meets the lock held waits for it rather than
// being lost.

import (
	"context"
	"testing"
	"time"
)

func spawnOf(r *Runtime, wi string) int {
	ji, _ := r.Info(wi)
	return ji.SpawnN
}

func TestOBLoopHungRecoveryDoesNotDelayOthers(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, fromOB := newTestRuntime(t, hub, &scriptedSpawner{})
	const a, b = "wi_ob_a", "wi_ob_b"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); r.Close() })
	startRunning(t, r, a)
	startRunning(t, r, b)
	release := stallPushes(t, r, a)
	go func() { _ = r.Run(ctx) }()

	spawnA, spawnB := spawnOf(r, a), spawnOf(r, b)
	msgA := recoveryMsg(r.jobOf(a), "agent exited")
	fromOB.Push(msgA)
	waitBusy(t, r, a)
	fromOB.Push(msgA) // a duplicate death signal of the recovering derivation
	fromOB.Push(recoveryMsg(r.jobOf(b), "agent exited"))
	waitFor(t, b+"'s recovery while "+a+"'s push hangs", func() bool {
		ji, ok := r.Info(b)
		return ok && ji.State == StateRunning && ji.SpawnN == spawnB+1
	})
	if ji, _ := r.Info(a); ji.State != StateRecovering {
		t.Fatalf("%s during its hung push: %s, want recovering", a, ji.State)
	}
	release()
	waitFor(t, a+"'s recovery once its push returned", func() bool {
		ji, ok := r.Info(a)
		return ok && ji.State == StateRunning && ji.SpawnN == spawnA+1
	})
	settleOps(t, r, 5*time.Second)
	if n := spawnOf(r, a); n != spawnA+1 {
		t.Fatalf("%s relaunched to spawn %d, want exactly one recovery (%d)", a, n, spawnA+1)
	}
}

// A death signal arriving while another operation of the work item holds
// its lock (a start finishing its bookkeeping, say) is not lost: the
// recovery runs once the lock is free.
func TestOBRecoveryWaitsForTheLock(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, fromOB := newTestRuntime(t, hub, &scriptedSpawner{})
	const a = "wi_ob_wait"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); r.Close() })
	startRunning(t, r, a)
	go func() { _ = r.Run(ctx) }()
	spawn := spawnOf(r, a)
	hold := make(chan struct{})
	r.post(a, func() { <-hold }) // another operation of the work item under way
	fromOB.Push(recoveryMsg(r.jobOf(a), "agent exited"))
	time.Sleep(100 * time.Millisecond)
	if n := spawnOf(r, a); n != spawn {
		t.Fatalf("the recovery ran while the work item's actor was busy (spawn %d)", n)
	}
	close(hold)
	waitFor(t, "the recovery once the lock was free", func() bool {
		ji, ok := r.Info(a)
		return ok && ji.State == StateRunning && ji.SpawnN == spawn+1
	})
}
