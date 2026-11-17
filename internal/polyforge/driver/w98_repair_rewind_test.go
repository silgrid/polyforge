package driver

// w98_repair_rewind_test.go (polyforge#98): the integration lane's
// repair route as the driver runs it — the full gate's FAIL rewinds to
// the rewind_only repair step charged to the lane's one max_rewinds
// budget (AC3: a repair round is a rewind like any other, never a free
// extra round), the review between them activates with it (the gate's
// red range [repair, full-gate) covers both), and the budget spent
// parks the next FAIL at the call — the gate's to take to the owner.
// The generic rewind_only semantics are rewind_only_test's; this pins
// the shipped lane's own edges.

import (
	"path/filepath"
	"strings"
	"testing"

	"encoding/json"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// w98Result is completedResult for this driver's own work item id.
func w98Result(stepID string, n int64, output string) *StepResult {
	return &StepResult{WIID: "wi_w98", StepID: stepID, SpawnN: n, Outcome: OutcomeCompleted, Output: json.RawMessage(output)}
}

// w98DriveRound drives one pass of the shipped integration lane up to
// the full gate and FAILs it: the rewind_only steps are skipped on the
// first round (the plan's own recording) and run on every later one
// (the FAIL before them archived them). It answers the next spawn
// number.
func w98DriveRound(t *testing.T, d *Driver, wf *Workflow, first bool, n int64) int64 {
	t.Helper()
	mustStart(t, d, "collect", n)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "collect", SpawnN: n,
		Result: w98Result("collect", n, `{}`)}); err != nil {
		t.Fatalf("result collect (%d): %v", n, err)
	}
	n++
	if first {
		for _, sid := range []string{"repair", "review"} {
			if err := d.transition(TransitionRequest{Kind: TransStepSkipped, StepID: sid, SpawnN: n - 1}); err != nil {
				t.Fatalf("skip %s (the first round's own recording): %v", sid, err)
			}
		}
	} else {
		for _, sid := range []string{"repair", "review"} {
			mustStart(t, d, sid, n)
			out := `{}`
			if sid == "review" {
				out = `{"verdict":"pass"}`
			}
			if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: sid, SpawnN: n,
				Result: w98Result(sid, n, out)}); err != nil {
				t.Fatalf("result %s (%d): %v", sid, n, err)
			}
			n++
		}
	}
	mustStart(t, d, "revert", n)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "revert", SpawnN: n,
		Result: w98Result("revert", n, `{}`)}); err != nil {
		t.Fatalf("result revert (%d): %v", n, err)
	}
	n++
	mustStart(t, d, "full-gate", n)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "full-gate", SpawnN: n,
		Result: &StepResult{StepID: "full-gate", SpawnN: n, Outcome: OutcomeGateFailed,
			Output: []byte(`{"verdict":"fail","fail_reason":"the gate is red"}`)}}); err != nil {
		t.Fatalf("gate_fail full-gate (%d): %v", n, err)
	}
	return n + 1
}

// TestW98RepairFailChargesAndActivates: on the shipped integration lane
// the full gate's FAIL rewinds to the repair step charged, both
// rewind_only steps run from the second round on, and max_rewinds 3
// spent parks the next FAIL (RewindBudgetSpent) — the gate asks the
// owner instead.
func TestW98RepairFailChargesAndActivates(t *testing.T) {
	wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, "integrate"))
	if err != nil {
		t.Fatalf("the shipped integration lane: %v", err)
	}
	dir := t.TempDir()
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_w98", CapsuleDir: dir, BundleName: "integrate"},
		ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
		Workflow: wf,
		Spawner:  &fakeSpawner{},
		Log:      &eventLog{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustInit(t, d)
	n := w98DriveRound(t, d, wf, true, 1)
	x := d.Snapshot()
	if x.RewindStats.Count != 1 || len(x.RewindStats.Rewinds) != 1 {
		t.Fatalf("the full gate's FAIL is one rewind: %+v", x.RewindStats)
	}
	if r := x.RewindStats.Rewinds[0]; r.Gate != "full-gate" || r.ToStep != "repair" || r.Class != capsule.RewindCharged {
		t.Fatalf("the FAIL rewinds to the repair step charged: %+v", r)
	}
	for _, sid := range []string{"repair", "review", "revert"} {
		if st := x.Steps[sid]; st == nil || st.Status != capsule.StepPending {
			t.Fatalf("%s must be reset to pending (the red range covers it): %+v", sid, st)
		}
	}
	if st := x.Steps["repair"]; len(st.Attempts) != 1 || st.Attempts[0].Cause != CauseGateFailRewind {
		t.Fatalf("the repair step's own archive (the activation): %+v", st.Attempts)
	}
	if st := x.Steps["review"]; len(st.Attempts) != 1 || st.Attempts[0].Cause != CauseSupersededByRewind {
		t.Fatalf("the review's own archive (the activation): %+v", st.Attempts)
	}
	// the second and third repair rounds run the two rewind_only steps
	n = w98DriveRound(t, d, wf, false, n)
	n = w98DriveRound(t, d, wf, false, n)
	x = d.Snapshot()
	if x.RewindStats.Count != 3 {
		t.Fatalf("three repair rounds spend three rewinds: %+v", x.RewindStats)
	}
	for _, r := range x.RewindStats.Rewinds {
		if r.Gate != "full-gate" || r.ToStep != "repair" || r.Class != capsule.RewindCharged {
			t.Fatalf("every repair round rewinds the same edge charged: %+v", x.RewindStats.Rewinds)
		}
	}
	for _, sid := range []string{"repair", "review"} {
		if st := x.Steps[sid]; st.Status != capsule.StepPending || len(st.Attempts) != 3 {
			t.Fatalf("%s ran on every rewound round (three archives, pending again): %q %+v", sid, st.Status, st.Attempts)
		}
	}
	// the budget is spent: the next FAIL not citing the owner is refused
	// at the call — the gate's route to the owner
	if why := RewindBudgetSpent(wf, x); why == "" || !strings.Contains(why, "max_rewinds 3") {
		t.Fatalf("max_rewinds 3 is spent after three repair rounds: %q %+v", why, x.RewindStats)
	}
}
