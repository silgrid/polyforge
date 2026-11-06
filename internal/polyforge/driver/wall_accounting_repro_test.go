package driver

// wall_accounting_repro_test.go: B4 — wall-time holes in the
// D6 cumulative-T ledger (the result_invalid block's left with the
// envelope, workflow contract 4):
//
//  2. an envelope a dead predecessor collected (archived, never settled)
//     is settled without booking the dead dispatch's runtime at all;
//  3. effectiveThresholdsLocked derives PriorRuntime from the persisted
//     BudgetConsumed only, so a rejected dispatch's not-yet-persisted
//     wall is invisible to the next dispatch's supervisor.
//
// Written RED against the current driver: each measured wall comes back
// zero (or absent from PriorRuntime).

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// 2. Settling an envelope collected by the dead predecessor must book the
// dead dispatch's runtime (the time since its step_start) into T.
func TestCollectedEnvelopeBooksTheDeadDispatchWall(t *testing.T) {
	e := newCrashEnv(t, spec("a"))
	// The dead driver collected/archived the envelope but never settled
	// it: the successor finds it under the step's archive.
	e.crashHeld(nil, func() {
		e.archived("a", 1, StepResult{
			Outcome: OutcomeCompleted,
			Output:  json.RawMessage(`{"x":1}`),
		})
	})

	// Pin the dead dispatch's start so the successor's clock makes the
	// measured runtime deterministic: it ran for 90 seconds.
	start := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	x := readExec(t, e.execPath)
	if st := x.Steps["a"]; st == nil || st.Status != capsule.StepInProgress || st.Dispatch != 1 {
		t.Fatalf("the crash must leave the dispatch unsettled: %+v", st)
	}
	x.Steps["a"].StepStartedAt = start.Format(time.RFC3339Nano)
	blob, err := json.MarshalIndent(x, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.execPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}

	sp := &fakeSpawner{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_crash", CapsuleDir: e.dir, BundleName: "b"},
		ExecPath: e.execPath,
		Workflow: e.wf,
		Spawner:  sp,
		SpawnN:   1,
		Now:      func() time.Time { return start.Add(90 * time.Second) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 0 {
		t.Fatalf("the collected envelope must settle without a re-dispatch: %v", got)
	}

	x2 := readExec(t, e.execPath)
	if x2.WorkflowStatus != StatusCompleted {
		t.Fatalf("the collected envelope must settle the step: %q", x2.WorkflowStatus)
	}
	if got := x2.Steps["a"].BudgetConsumed.WallMs; got < int64(60*time.Second/time.Millisecond) {
		t.Fatalf("the dead dispatch's runtime must be booked into T, got %dms", got)
	}
}

// 3. A dispatch that finished but whose wall is not yet persisted (a
// rejected envelope) is still prior runtime: the next dispatch's
// supervisor thresholds must carry it.
func TestSupervisorPriorRuntimeIncludesUnpersistedRejectedWall(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)

	d.mu.Lock()
	d.unbooked["a"] = int64(7 * time.Second / time.Millisecond)
	d.mu.Unlock()

	th := d.effectiveThresholdsLocked("a", supervise.GranularityToken)
	if th.PriorRuntime < 7*time.Second {
		t.Fatalf("PriorRuntime %v, want at least the 7s of unpersisted (rejected) dispatch wall", th.PriorRuntime)
	}
}
