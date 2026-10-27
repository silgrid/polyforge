package daemon

// autowave_b3_gap_repro_test.go: the two B3 gaps polyforge#151 keeps
// open on top of the already-merged B3a/B3b fixes (see
// autowave_b3_repro_test.go for those):
//
//  1. race: fetchAutoWave checks the auto switch ONCE, before pulling
//     the ready queue (daemon.go: AdmitAutoPull at the top). When `pf
//     ops auto off` lands while the wave is mid-fetch, this wave still
//     enqueues origin=auto entries afterwards — entries "auto off"
//     was supposed to prevent (v2-converge §5).
//  2. quota: the wave's free-slot budget (FreeSlots) counts only
//     RUNNING jobs; entries already queued (including ones held back in
//     their DAEMON-503 backoff) are not deducted, so a wave can enqueue
//     past max_concurrent_jobs. The budget must be free slots minus
//     already-queued entries.
//
// Both tests fail before the fix, for exactly the reported reasons.

import (
	"fmt"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// TestAutoOffDuringQueueFetchStopsTheWave: the operator flips auto off
// while a wave is parked inside the hub's ready-queue fetch. Expected:
// nothing origin=auto is enqueued by that wave. Observed before the fix:
// the wave had already passed the one switch check and enqueues anyway.
func TestAutoOffDuringQueueFetchStopsTheWave(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	seedReadyB3(t, td, 3)

	// park the wave inside the hub fetch: Queue blocks until released,
	// and entered fires once a Queue call has parked.
	entered := make(chan struct{})
	block := make(chan struct{})
	td.hub.mu.Lock()
	td.hub.queueBlock = block
	td.hub.queueEntered = entered
	td.hub.mu.Unlock()

	done := make(chan struct{})
	go func() {
		td.fetchAutoWave()
		close(done)
	}()
	<-entered // the wave passed its AdmitAutoPull check (auto was on) and is mid-fetch

	// the operator turns auto off THROUGH THE OPS FACE while the fetch is in flight.
	off := false
	if _, err := td.client.OpsConfig(cli.OpsConfigRequest{Auto: &off}); err != nil {
		t.Fatalf("ops auto off: %v", err)
	}

	close(block) // the fetch answers; the wave's enqueue loop runs now
	<-done

	if got := td.q.LenPending(); got != 0 {
		t.Fatalf("REPORTED FAILURE (B3 race): auto was turned off while the wave was fetching the ready queue, yet the wave still enqueued %d origin=auto entries (v2-converge §5: auto off = only explicit pf submit; the switch must be re-checked before each enqueue)", got)
	}
}

// TestAutoWaveBudgetSubtractsQueuedEntries: max 4 concurrent, no
// running jobs, 3 entries already queued and waiting out their backoff.
// Expected: the wave's budget is 4 free - 3 queued = 1, so one wave
// enqueues at most ONE more entry (pending stays at 4). Observed before
// the fix: the 3 queued entries are not deducted and the wave enqueues 4.
func TestAutoWaveBudgetSubtractsQueuedEntries(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.MaxConcurrent = 4
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	// three auto entries already on the queue, held back in their
	// DAEMON-503 backoff (NotBefore in the future: Take skips them, they
	// occupy no running slot, but they are waiting for a slot).
	for i := 1; i <= 3; i++ {
		e := qEntry{
			WorkItemID: fmt.Sprintf("wi_b3q_%d", i),
			Priority:   api.PriorityNormal,
			Origin:     runtime.Origin{Kind: runtime.OriginAuto},
			NotBefore:  time.Now().Add(time.Hour),
		}
		if err := td.q.Enqueue(e); err != nil {
			t.Fatalf("seed queued entry: %v", err)
		}
	}
	seedReadyB3(t, td, 5) // five MORE ready work items (wi_b3_1..5)

	td.fetchAutoWave()

	// 3 queued + budget (4 free - 3 queued = 1) = 4 pending at most.
	if got := td.q.LenPending(); got != 4 {
		t.Fatalf("REPORTED FAILURE (B3 quota): with 4 slots, 0 running and 3 entries already queued (in backoff), one auto wave left %d pending entries — the free-slot budget was not reduced by the already-queued entries (want 4: 3 queued + 1 new)", got)
	}
}
