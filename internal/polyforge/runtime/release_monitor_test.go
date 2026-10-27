package runtime

// release_monitor_test.go — form ①: a close that ends
// the session must release the work item's observe monitor (stopped,
// its in-flight push drained: Config.ReleaseMonitor → Observe.Release)
// BEFORE its SessionEnd. Before the fix the monitor was stopped only by
// the RTFinished sent after SessionEnd, so its redundant terminal re-push
// ran against the ended session and was recorded as an "Authentication
// failed" GIT-500 push_failed (pi#126, 26 of 112 terminals).

import (
	"context"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// releaseRecorder binds Config.ReleaseMonitor and records, per call, how
// many SessionEnd calls the hub had already seen.
func releaseRecorder(r *Runtime, hub *fakeHub) func() []int {
	var mu sync.Mutex
	var endsSeen []int
	r.cfg.ReleaseMonitor = func(string) {
		hub.mu.Lock()
		n := len(hub.ends)
		hub.mu.Unlock()
		mu.Lock()
		endsSeen = append(endsSeen, n)
		mu.Unlock()
	}
	return func() []int {
		mu.Lock()
		defer mu.Unlock()
		return append([]int(nil), endsSeen...)
	}
}

func TestTerminalCloseReleasesTheMonitorBeforeSessionEnd(t *testing.T) {
	const wi = "wi_relclose"
	r, hub, _ := sealRetryRuntime(t, wi)
	calls := releaseRecorder(r, hub)
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	hub.mu.Lock()
	ends := len(hub.ends)
	hub.mu.Unlock()
	if ends != 1 {
		t.Fatalf("SessionEnd calls = %d, want 1", ends)
	}
	if got := calls(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("monitor releases (SessionEnds seen at each) = %v, want exactly one, before SessionEnd ([0])", got)
	}
}

func TestBoundedFailReleasesTheMonitorBeforeSessionEnd(t *testing.T) {
	hub := newFakeHub()
	r, j, _ := endPendingRuntime(t, hub)
	calls := releaseRecorder(r, hub)
	underLock(r, j.wi, func() { r.boundedFail(context.Background(), j, "boom") })
	hub.mu.Lock()
	ends := len(hub.ends)
	hub.mu.Unlock()
	if ends != 1 {
		t.Fatalf("SessionEnd calls = %d, want 1", ends)
	}
	if got := calls(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("monitor releases (SessionEnds seen at each) = %v, want exactly one, before SessionEnd ([0])", got)
	}
}
