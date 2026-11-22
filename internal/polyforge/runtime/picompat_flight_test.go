package runtime

// The shared flight's abandonment boundary (owner ruling Q4; review r2#1):
// when the last waiter leaves, the flight is cancelled but stays in the map
// until its probe has RETURNED — the next same-key caller must not start a
// second probe while the abandoned one is still reaping its process group.
// A cancelled waiter also never cancels a flight another waiter still
// waits on (review r2#6).

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// flightProbe tracks how many capability calls run at the same time and the
// order they start in.
type flightProbe struct {
	mu          sync.Mutex
	active      int
	maxActive   int
	probes      int
	secondEarly bool
}

func (p *flightProbe) enter() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active++
	p.probes++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	return p.probes
}

func (p *flightProbe) leave() {
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
}

func (p *flightProbe) snapshot() (active, maxActive, probes int, secondEarly bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active, p.maxActive, p.probes, p.secondEarly
}

// flightWaitFor polls (bounded) until cond holds, then answers whether it did.
func flightWaitFor(deadline time.Duration, cond func() bool) bool {
	until := time.Now().Add(deadline)
	for time.Now().Before(until) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestPiCompatibilityAbandonedFlightIsReapedBeforeTheNextProbe(t *testing.T) {
	resetPiCompatCache()
	t.Cleanup(resetPiCompatCache)
	oldFS := piCompatResourceFS
	piCompatResourceFS = compatResourceMap(t)
	t.Cleanup(func() { piCompatResourceFS = oldFS })
	oldProbe := piCapabilityProbeFn
	t.Cleanup(func() { piCapabilityProbeFn = oldProbe })

	bin, _ := compatCountingPi(t)
	tracker := &flightProbe{}
	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	firstReturned := make(chan struct{})
	piCapabilityProbeFn = func(ctx context.Context, bin, version string) error {
		n := tracker.enter()
		defer tracker.leave()
		if n == 1 {
			select {
			case firstStarted <- struct{}{}:
			default:
			}
			<-ctx.Done()
			// the real probe's bounded process-group reaping
			<-releaseFirst
			close(firstReturned)
			return ctx.Err()
		}
		select {
		case <-firstReturned:
		default:
			tracker.mu.Lock()
			tracker.secondEarly = true
			tracker.mu.Unlock()
		}
		return nil
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	first := make(chan error, 1)
	go func() { first <- validatePiCompatibility(ctx1, bin) }()
	select {
	case <-firstStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("the first capability probe never started")
	}
	cancel1()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the cancelled caller returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled caller never returned")
	}

	// the same key, immediately: while the abandoned probe is still reaping,
	// no second probe may start.
	second := make(chan error, 1)
	go func() { second <- validatePiCompatibility(context.Background(), bin) }()
	// a bounded observation window: a second probe, if it wrongly started,
	// appears within it; the assertions below then read the settled counts.
	flightWaitFor(500*time.Millisecond, func() bool {
		_, _, probes, _ := tracker.snapshot()
		return probes >= 2
	})
	close(releaseFirst)
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("the call after an abandoned flight failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the second caller never returned")
	}
	active, maxActive, probes, secondEarly := tracker.snapshot()
	if active != 0 {
		t.Errorf("capability probes still running after both calls returned: %d", active)
	}
	if maxActive != 1 {
		t.Errorf("%d capability probes ran at the same time; an abandoned flight must finish reaping before the next same-key probe starts", maxActive)
	}
	if secondEarly {
		t.Error("the second probe started before the abandoned one returned")
	}
	if probes != 2 {
		t.Errorf("the same key probed %d time(s) in total, want 2 (the abandoned flight is not cached)", probes)
	}
}

func TestPiCompatibilityOneWaiterCancelKeepsTheFlightForTheOthers(t *testing.T) {
	resetPiCompatCache()
	t.Cleanup(resetPiCompatCache)
	oldFS := piCompatResourceFS
	piCompatResourceFS = compatResourceMap(t)
	t.Cleanup(func() { piCompatResourceFS = oldFS })
	oldProbe := piCapabilityProbeFn
	t.Cleanup(func() { piCapabilityProbeFn = oldProbe })

	bin, _ := compatCountingPi(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var probes atomic.Int32
	var probeCtxCanceled atomic.Bool
	piCapabilityProbeFn = func(ctx context.Context, bin, version string) error {
		probes.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		if ctx.Err() != nil {
			probeCtxCanceled.Store(true)
		}
		return nil
	}

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	a := make(chan error, 1)
	b := make(chan error, 1)
	go func() { a <- validatePiCompatibility(ctxA, bin) }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the capability probe never started")
	}
	go func() { b <- validatePiCompatibility(ctxB, bin) }()
	// the second caller must have joined the same flight before A leaves:
	// poll on the probe count staying 1 while B is blocked.
	time.Sleep(20 * time.Millisecond)
	cancelA()
	select {
	case err := <-a:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the cancelled waiter returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled waiter never returned")
	}
	close(release)
	select {
	case err := <-b:
		if err != nil {
			t.Fatalf("the remaining waiter lost the flight: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the remaining waiter never returned")
	}
	if probeCtxCanceled.Load() {
		t.Error("one waiter's cancel cancelled the flight another waiter still awaited")
	}
	if got := probes.Load(); got != 1 {
		t.Errorf("the same-key callers probed %d time(s), want 1", got)
	}
}
