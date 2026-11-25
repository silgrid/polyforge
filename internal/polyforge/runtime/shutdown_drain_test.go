package runtime

// shutdown_drain_test.go — the shutdown closes the actors' entry and
// Drain waits for what was already accepted (polyforge#278): an operation
// accepted before the shutdown runs to the end of its persist, Drain
// returns only once it has, and an operation posted after the shutdown is
// refused with errStopped at once instead of waiting or running.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitClosed fails the test when ch is not closed within d.
func waitClosed(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("%s did not happen within %s", what, d)
	}
}

// recvWithin answers the first error ch delivers, failing the test when
// none comes within d.
func recvWithin(t *testing.T, ch <-chan error, d time.Duration, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(d):
		t.Fatalf("%s did not return within %s", what, d)
		return nil
	}
}

func TestDrainWaitsForAcceptedOperationsAndRefusesNewOnes(t *testing.T) {
	r, _, _ := newTestRuntime(t, newFakeHub(), &scriptedSpawner{})
	if err := os.MkdirAll(r.cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seedJob(r, "wi_dr_a")

	entered := make(chan struct{})
	release := make(chan struct{})
	stop := make(chan struct{})
	var releaseOnce, stopOnce sync.Once
	var posters sync.WaitGroup
	// Registered after newTestRuntime's cleanup, so it runs first: a
	// failed assertion still lets the held operation and the posters end
	// before the runtime's own cleanup settles the actors.
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		stopOnce.Do(func() { close(stop) })
		done := make(chan struct{})
		go func() { posters.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("the poster goroutines did not stop within 10s of the cleanup")
		}
	})

	// The accepted, slow operation: it runs on wi_dr_a's actor, and its
	// persist is held behind the release barrier.
	held := make(chan error, 1)
	go func() {
		held <- r.call(context.Background(), "wi_dr_a", func() error {
			close(entered)
			<-release
			seedJob(r, "wi_dr_b")
			return r.persist()
		})
	}()

	// Posters race the shutdown: each keeps calling until it is refused,
	// so -race sees the actors' registration against the drain's wait.
	const nPosters = 8
	posterDeadline := time.Now().Add(20 * time.Second)
	var refused [nPosters]atomic.Bool
	for k := 0; k < nPosters; k++ {
		posters.Add(1)
		go func(k int) {
			defer posters.Done()
			wi := fmt.Sprintf("wi_dr_p%d", k)
			for time.Now().Before(posterDeadline) {
				select {
				case <-stop:
					return
				default:
				}
				if err := r.call(context.Background(), wi, r.persist); errors.Is(err, errStopped) {
					refused[k].Store(true)
					return
				}
			}
		}(k)
	}

	waitClosed(t, entered, 5*time.Second, "the held operation's start")
	within(t, 5*time.Second, "Close", r.Close)

	// A: the held operation was accepted and has not persisted yet, so a
	// drain bounded at 300ms runs out.
	drainA := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		drainA <- r.Drain(ctx)
	}()
	if err := recvWithin(t, drainA, 5*time.Second, "Drain (300ms deadline)"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("assertion A: Drain with an accepted operation still held = %v, want context.DeadlineExceeded", err)
	}

	// B: after Close a new operation is refused at once and never runs —
	// on a work item without a mailbox and on one whose actor is busy.
	for _, wi := range []string{"wi_dr_c", "wi_dr_a"} {
		var ran atomic.Bool
		got := make(chan error, 1)
		go func() {
			got <- r.call(context.Background(), wi, func() error { ran.Store(true); return nil })
		}()
		if err := recvWithin(t, got, 5*time.Second, "a call on "+wi+" after Close"); !errors.Is(err, errStopped) {
			t.Fatalf("assertion B: a call on %s after Close = %v, want errStopped", wi, err)
		}
		if ran.Load() {
			t.Fatalf("assertion B: the operation posted on %s after Close ran", wi)
		}
	}

	// C: once released, the held operation finishes, and so does Drain.
	releaseOnce.Do(func() { close(release) })
	drainC := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		drainC <- r.Drain(ctx)
	}()
	if err := recvWithin(t, drainC, 10*time.Second, "Drain (5s deadline)"); err != nil {
		t.Fatalf("assertion C: Drain after the release = %v, want nil", err)
	}
	if err := recvWithin(t, held, 5*time.Second, "the held call"); err != nil {
		t.Fatalf("assertion C: the held operation = %v, want nil", err)
	}
	if !r.Idle() {
		t.Fatalf("assertion C: an actor still has work after Drain returned nil")
	}
	postersDone := make(chan struct{})
	go func() { posters.Wait(); close(postersDone) }()
	waitClosed(t, postersDone, 5*time.Second, "every poster's exit")
	for k := range refused {
		if !refused[k].Load() {
			t.Fatalf("assertion C: poster %d stopped without being refused errStopped", k)
		}
	}

	// D: the held persist landed whole — both seeded work items are on
	// disk and no temp file of the atomic write is left.
	ids := map[string]bool{}
	for _, rec := range loadRecords(t, r) {
		ids[rec.WorkItemID] = true
	}
	if !ids["wi_dr_a"] || !ids["wi_dr_b"] {
		t.Fatalf("assertion D: runtime-state.json records %v, want wi_dr_a and wi_dr_b", ids)
	}
	tmps, err := filepath.Glob(filepath.Join(r.cfg.DataDir, ".runtime-state.json.*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tmps) != 0 {
		t.Fatalf("assertion D: temp files left after the drain: %v", tmps)
	}
}
