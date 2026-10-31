package driver

// lane_contract_test.go: pins the shipped lanes' contract
// after the reorder — the three review-bearing delivery lanes run
// code → review → verification → deliver, autopilot keeps its order, the
// rewind bounds are 9/3 (autopilot 6/3), and per-edge charging behaves
// identically on a synthetic lane no workbench name ever touches (the
// generality evidence the platform-layer rule demands).

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// laneOrder answers the lane's step ids in declaration order.
func laneOrder(t *testing.T, bundle string) []string {
	t.Helper()
	wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, bundle))
	if err != nil {
		t.Fatalf("%s: %v", bundle, err)
	}
	out := make([]string, 0, len(wf.Steps))
	for _, s := range wf.Steps {
		out = append(out, s.ID)
	}
	return out
}

// TestShippedLanesReviewPrecedesVerification: the three review-bearing
// delivery lanes declare review before verification (the frontmatter is
// the only order authority); autopilot keeps grill → fix → verification →
// deliver.
func TestShippedLanesReviewPrecedesVerification(t *testing.T) {
	for _, bundle := range []string{"feature-full", "chore", "bug-fix"} {
		order := laneOrder(t, bundle)
		pos := map[string]int{}
		for i, id := range order {
			pos[id] = i
		}
		ri, okR := pos[reviewStepID]
		vi, okV := pos[verificationStepID]
		if !okR || !okV {
			t.Fatalf("%s: no review/verification steps: %v", bundle, order)
		}
		if ri >= vi {
			t.Errorf("%s: review (%d) must precede verification (%d): %v", bundle, ri, vi, order)
		}
	}
	if got := laneOrder(t, "autopilot"); len(got) != 4 ||
		got[0] != "grill" || got[1] != "fix" || got[2] != verificationStepID || got[3] != deliverStepID {
		t.Fatalf("autopilot's order must stay grill → fix → verification → deliver, got %v", got)
	}
}

// TestShippedLaneRewindBounds: the delivery lanes carry 9/3, autopilot 6/3.
func TestShippedLaneRewindBounds(t *testing.T) {
	for _, bundle := range []string{"feature-full", "chore", "bug-fix"} {
		wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, bundle))
		if err != nil {
			t.Fatalf("%s: %v", bundle, err)
		}
		if wf.MaxTotal != 9 || wf.MaxPerStep != 3 {
			t.Errorf("%s: bounds %d/%d, want 9/3", bundle, wf.MaxTotal, wf.MaxPerStep)
		}
	}
	wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, "autopilot"))
	if err != nil {
		t.Fatal(err)
	}
	if wf.MaxTotal != 6 || wf.MaxPerStep != 3 {
		t.Fatalf("autopilot: bounds %d/%d, want 6/3", wf.MaxTotal, wf.MaxPerStep)
	}
}

// driveGateRound walks the lane to one gate FAIL: every step in
// [target, gate) completes (gates with a pass verdict), then the gate
// FAILs — one charged rewind of that gate→target edge. It answers the
// next spawn ordinal to use.
func driveGateRound(t *testing.T, d *Driver, wf *Workflow, gate string, n int64) int64 {
	t.Helper()
	gIdx, _ := wf.indexOf(gate)
	target := wf.step(gate).OnFail.Target
	tIdx, _ := wf.indexOf(target)
	for i := tIdx; i < gIdx; i++ {
		sid := wf.Steps[i].ID
		mustStart(t, d, sid, n)
		out := `{}`
		if wf.Steps[i].Gate {
			out = `{"verdict":"pass"}`
		}
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: sid, SpawnN: n,
			Result: &StepResult{WIID: d.wi.ID, StepID: sid, SpawnN: n, Outcome: OutcomeCompleted, Output: json.RawMessage(out)},
		}); err != nil {
			t.Fatalf("complete %s (%s, round at %d): %v", sid, gate, n, err)
		}
		n++
	}
	mustStart(t, d, gate, n)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: gate, SpawnN: n,
		Result: &StepResult{WIID: d.wi.ID, StepID: gate, SpawnN: n, Outcome: OutcomeGateFailed,
			Output: json.RawMessage(`{"verdict":"fail","fail_reason":"contract round"}`)},
	}); err != nil {
		t.Fatalf("fail %s (round %d): %v", gate, n, err)
	}
	return n + 1
}

// TestShippedLaneExhaustionIsConstructive: for every shipped lane and
// every gate→target edge, a constructive sequence uses the edge's three
// charged slots (three rewinds land) and the NEXT charged FAIL on that
// edge terminates the workflow — never an infinite loop — while the
// lane-wide invariant (#edges × max_per_step ≥ max_total) holds, so
// max_total is reachable through charged rewinds alone.
func TestShippedLaneExhaustionIsConstructive(t *testing.T) {
	for _, bundle := range []string{"feature-full", "chore", "bug-fix", "autopilot"} {
		wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, bundle))
		if err != nil {
			t.Fatalf("%s: %v", bundle, err)
		}
		edges := 0
		for _, s := range wf.Steps {
			if s.OnFail != nil {
				edges++
			}
		}
		if edges*wf.MaxPerStep < wf.MaxTotal {
			t.Errorf("%s: max_total %d unreachable through %d edges x max_per_step %d",
				bundle, wf.MaxTotal, edges, wf.MaxPerStep)
		}
		for _, s := range wf.Steps {
			if s.OnFail == nil {
				continue
			}
			t.Run(bundle+"/"+s.ID, func(t *testing.T) {
				dir := t.TempDir()
				d, err := New(Config{
					WI:       WIInfo{ID: "wi_lc", CapsuleDir: dir, BundleName: bundle},
					ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
					Workflow: wf,
					Spawner:  &fakeSpawner{},
					Log:      &eventLog{},
				})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				mustInit(t, d)
				n := int64(1)
				if s.ID == rootCauseStepID {
					// the bug-fix dead end (spec: a second inconclusive
					// root cause asks the owner): one charged rewind, then
					// the next FAIL parks the owner question — never a
					// second charged rewind, never exhaustion.
					n = driveGateRound(t, d, wf, s.ID, n)
					driveGateRound(t, d, wf, s.ID, n)
					x := d.Snapshot()
					q := x.Steps[s.ID].PendingQuestion
					if x.WorkflowStatus != StatusRunning || x.RewindStats.Count != 1 || q == nil || q.ID != RootCauseInconclusiveQuestionID {
						t.Fatalf("root-cause: the second FAIL must park the owner question, got %q count %d question %+v",
							x.WorkflowStatus, x.RewindStats.Count, q)
					}
					return
				}
				for round := 0; round < wf.MaxPerStep; round++ {
					n = driveGateRound(t, d, wf, s.ID, n)
					if x := d.Snapshot(); x.RewindStats.Count != int64(round+1) {
						t.Fatalf("%s: round %d: count %d, want %d", s.ID, round, x.RewindStats.Count, round+1)
					}
				}
				// the edge is full: the next charged FAIL terminates before
				// any mutation.
				before := d.Snapshot()
				driveGateRound(t, d, wf, s.ID, n)
				x := d.Snapshot()
				if x.WorkflowStatus != StatusFailedRewindExhausted || x.RewindStats.Count != before.RewindStats.Count {
					t.Fatalf("%s: the edge's next charged FAIL must terminate at count %d, got %q count %d",
						s.ID, before.RewindStats.Count, x.WorkflowStatus, x.RewindStats.Count)
				}
				for _, r := range x.RewindStats.Rewinds {
					if r.Gate != s.ID || r.ToStep != s.OnFail.Target || r.Class != capsule.RewindCharged {
						t.Fatalf("%s: every landed rewind must attribute to its edge: %+v", s.ID, x.RewindStats.Rewinds)
					}
				}
			})
		}
	}
}

// TestSyntheticLaneChargesPerEdgeToo: the per-edge quota is driver-level —
// a synthetic lane (a bundle name no workbench project ever touches, an
// arbitrary repo/task vocabulary) charges exactly like the shipped ones:
// two gates sharing one target carry their own max_per_step slots, and an
// edge at its bound never refuses the sibling's or an uncharged rewind.
func TestSyntheticLaneChargesPerEdgeToo(t *testing.T) {
	wf := &Workflow{
		Steps: []StepSpec{
			{ID: "compose", ContextPolicy: ContextFresh, Handoff: true},
			{ID: "craft", ContextPolicy: ContextInherit},
			gateSpec("inspect", "craft"),
			gateSpec("audit", "craft"),
		},
		MaxTotal: 4, MaxPerStep: 2,
	}
	if err := validateWorkflow(wf); err != nil {
		t.Fatalf("the synthetic lane must validate: %v", err)
	}
	dir := t.TempDir()
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_synth", CapsuleDir: dir, BundleName: "synth-relay"},
		ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
		Workflow: wf,
		Spawner:  &fakeSpawner{},
		Log:      &eventLog{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustInit(t, d)
	n := int64(1)
	for round := 0; round < 2; round++ {
		n = driveGateRound(t, d, wf, "inspect", n)
	}
	for round := 0; round < 2; round++ {
		n = driveGateRound(t, d, wf, "audit", n) // the sibling's quota is untouched by inspect's full edge
	}
	x := d.Snapshot()
	if x.RewindStats.Count != 4 || x.WorkflowStatus != StatusRunning {
		t.Fatalf("both edges' two slots must land: %q %+v", x.WorkflowStatus, x.RewindStats)
	}
	n = driveGateRound(t, d, wf, "inspect", n) // inspect's third: the edge bound
	x = d.Snapshot()
	if x.WorkflowStatus != StatusFailedRewindExhausted || x.RewindStats.Count != 4 {
		t.Fatalf("the full edge must terminate its own gate's next FAIL: %q %+v", x.WorkflowStatus, x.RewindStats)
	}
	for i, want := range []string{"inspect", "inspect", "audit", "audit"} {
		if x.RewindStats.Rewinds[i].Gate != want {
			t.Fatalf("rewind %d attributed to %q, want %q", i, x.RewindStats.Rewinds[i].Gate, want)
		}
	}
}
