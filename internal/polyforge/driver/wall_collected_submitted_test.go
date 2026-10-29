package driver

// wall_collected_submitted_test.go: B4 (D6 follow-up) — the
// collected-envelope booking ends at the envelope's daemon stamp, so the
// daemon downtime before the successor settles it is NOT counted as wall.

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestCollectedEnvelopeWallEndsAtTheSubmittedStamp(t *testing.T) {
	e := newCrashEnv(t, spec("a"))
	start := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	// The dead dispatch ran for 90 seconds and delivered; the daemon was
	// down for another hour before this successor collected the envelope.
	e.crashHeld(nil, func() {
		e.archived("a", 1, StepResult{
			Outcome:     OutcomeCompleted,
			Output:      json.RawMessage(`{"x":1}`),
			SubmittedAt: start.Add(90 * time.Second),
		})
	})
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
		Now:      func() time.Time { return start.Add(time.Hour) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	x2 := readExec(t, e.execPath)
	if x2.WorkflowStatus != StatusCompleted {
		t.Fatalf("the collected envelope must settle the step: %q", x2.WorkflowStatus)
	}
	want := int64(90 * time.Second / time.Millisecond)
	if got := x2.Steps["a"].BudgetConsumed.WallMs; got != want {
		t.Fatalf("the booking must end at submitted_at (%dms), got %dms — the daemon downtime was counted", want, got)
	}
}
