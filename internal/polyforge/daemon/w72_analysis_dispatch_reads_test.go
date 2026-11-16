// carries: internal/polyforge/daemon/analysis_queue.go

package daemon

// w72_analysis_dispatch_reads_test.go (polyforge#72, u16 — review r1#4):
// while the task lane cannot take the queued analysis (its slots full,
// the execution plane disabled, a rotation draining), the dispatch walk
// must not read the queued ticket from aihub on every tick (200ms) — the
// lane gate comes before the read, and the cleanup read of a blocked
// queue (AC2: drop a terminal or analyzed ticket's entry) runs at most
// once per DrainInterval, the period of the daemon's other hub polling.

import (
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// w72CountHub counts GetWorkItem calls per id.
type w72CountHub struct {
	*w72Hub
	cmu   sync.Mutex
	reads map[string]int
}

func (h *w72CountHub) GetWorkItem(id string) (*api.WorkItem, error) {
	h.cmu.Lock()
	h.reads[id]++
	h.cmu.Unlock()
	return h.w72Hub.GetWorkItem(id)
}

func (h *w72CountHub) readsOf(id string) int {
	h.cmu.Lock()
	defer h.cmu.Unlock()
	return h.reads[id]
}

func TestW72BlockedQueueDoesNotReadHubEveryTick(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		fill   bool
	}{
		{"slots full", nil, true},
		{"execution disabled", func(c *Config) { c.ExecutionDisabled = "the test disabled it" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var counter *w72CountHub
			td, hub, _ := w72World(t, func(c *Config) {
				c.DrainInterval = time.Hour
				counter = &w72CountHub{w72Hub: c.Hub.(*w72Hub), reads: map[string]int{}}
				c.Hub = counter
				if tc.mutate != nil {
					tc.mutate(c)
				}
			})
			if tc.fill {
				w72FillSlots(t, td, 2)
			}
			w72Ticket(t, hub, "wi_w72_wait", "## 背景\n\nx\n")
			if res := w72Enqueue(t, td, "wi_w72_wait"); res.State != "queued" {
				t.Fatalf("the blocked lane queues it: %+v", res)
			}
			base := counter.readsOf("wi_w72_wait")
			// a rate over a fixed window: the test daemon's dispatch tick is
			// 10ms, so 300ms is ~30 walks (the walk's own TryLock makes a
			// direct call count unobservable); the window bounds the check
			for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
				td.dispatchAnalysis()
				time.Sleep(10 * time.Millisecond)
			}
			// at most one cleanup read within one DrainInterval
			if n := counter.readsOf("wi_w72_wait") - base; n > 1 {
				t.Fatalf("~30 dispatch walks on a blocked lane read the queued ticket %d times, want at most 1 per DrainInterval", n)
			}
		})
	}
}
