package daemon

// Regression tests for wi_WLczIJMOLDhq item B3 (2026-10-13 audit): the auto
// wave (fetchAutoWave) enqueues the ENTIRE ready queue in one pass, and
// `pf ops auto off` leaves not-yet-started origin=auto entries sitting
// in the submit queue.
//
// Reported failures:
//  1. with one free concurrency slot and five ready work items, one
//     drain wave enqueues all five — the concurrency cap is checked
//     once (SlotFree) before the loop instead of bounding the wave;
//  2. auto off only flips the switch (SetAuto); pending origin=auto
//     entries survive and start later, although "auto off = only
//     explicit pf submit" (v2-converge §5) — explicit (cli/chat)
//     entries must be untouched.
//
// Regression tests: before the fix both symptoms reproduced.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// seedReady scripts n ready work items on the fake hub's queue.
func seedReadyB3(t *testing.T, td *testDaemon, n int) {
	t.Helper()
	td.hub.mu.Lock()
	for i := 1; i <= n; i++ {
		bundle := "demo-bundle"
		td.hub.queues = append(td.hub.queues, api.WorkItem{
			ID:          fmt.Sprintf("wi_b3_%d", i),
			ProjectName: "proj",
			Priority:    api.PriorityNormal,
			BundleName:  &bundle,
		})
	}
	td.hub.mu.Unlock()
}

// The wave must enqueue at most the CURRENT free-slot count: 1 free
// slot + 5 ready items ⇒ exactly 1 entry lands on the queue.
func TestAutoWaveEnqueuesAtMostFreeSlots(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.MaxConcurrent = 1
		// the loops under test are driven by hand; keep them out
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	seedReadyB3(t, td, 5)

	td.fetchAutoWave()

	if got := td.q.LenPending(); got != 1 {
		t.Fatalf("REPORTED FAILURE (B3): one auto wave with 1 free slot and 5 ready work items enqueued %d entries, want at most the 1 free slot", got)
	}
}

// queueFileEntries reads the persisted submit queue (the operator-visible
// state a restart replays).
func queueFileEntries(t *testing.T, td *testDaemon) []struct {
	WorkItemID string `json:"work_item_id"`
	Origin     struct {
		Kind string `json:"kind"`
	} `json:"origin"`
} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(td.cfg.DataDir, "submit-queue.json"))
	if err != nil {
		t.Fatalf("read submit queue: %v", err)
	}
	var st struct {
		Entries []struct {
			WorkItemID string `json:"work_item_id"`
			Origin     struct {
				Kind string `json:"kind"`
			} `json:"origin"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("submit queue undecodable: %v", err)
	}
	return st.Entries
}

// `pf ops auto off` must remove the queue's not-yet-started origin=auto
// entries; explicit (cli) submits ride untouched.
func TestAutoOffClearsPendingAutoEntries(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	seedReadyB3(t, td, 3)
	td.fetchAutoWave()
	if got := td.q.LenPending(); got != 3 {
		t.Fatalf("precondition: the wave should queue the 3 ready items, got %d", got)
	}

	// one EXPLICIT submit rides alongside — auto off must not touch it.
	if _, err := td.client.ClaimWithModels("wi_b3_manual", "normal", nil); err != nil {
		t.Fatalf("explicit claim: %v", err)
	}

	// the operator turns auto off through the ops face (pf ops auto off).
	off := false
	if _, err := td.client.OpsConfig(cli.OpsConfigRequest{Auto: &off}); err != nil {
		t.Fatalf("ops auto off: %v", err)
	}

	var auto, manual int
	for _, e := range queueFileEntries(t, td) {
		switch e.Origin.Kind {
		case runtime.OriginAuto:
			auto++
		case runtime.OriginChat:
			manual++ // the explicit submit is a chat origin (chat-less: empty ChatID)
		}
	}
	if auto != 0 {
		t.Fatalf("REPORTED FAILURE (B3): pf ops auto off left %d not-yet-started origin=auto entries on the queue (v2-converge §5: auto off = only explicit pf submit)", auto)
	}
	if manual != 1 {
		t.Fatalf("the explicit submit must survive auto off untouched, got %d cli entries", manual)
	}
}
