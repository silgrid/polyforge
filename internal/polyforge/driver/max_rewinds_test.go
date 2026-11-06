package driver

// max_rewinds_test.go: `max_rewinds: <n>` is the lane's one rewind budget
// across every gate→target edge (contract 1: the per-edge pair is gone).

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

const twoGateSteps = "steps:\n  - { id: build }\n  - { id: check-a, gate: true, on_fail: { target: build } }\n  - { id: check-b, gate: true, on_fail: { target: build } }\n"

// A lane with max_rewinds: 2 exhausts after two FAILs across DIFFERENT
// gates.
func TestMaxRewindsExhaustsAcrossGates(t *testing.T) {
	bundle := workflowtest.WriteLane(t, t.TempDir(), "two-gates", "max_rewinds: 2\n"+twoGateSteps)
	wf, err := LoadWorkflow(bundle)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	d, _, _, execPath := testEnv(t, wf.Steps...)
	d.workflow = wf
	mustInit(t, d)
	driveEdge(t, d, "build", "check-a", 1)
	driveEdge(t, d, "build", "check-b", 3)
	if x := readExec(t, execPath); x.WorkflowStatus != StatusRunning || x.RewindStats.Count != 2 {
		t.Fatalf("two rewinds across two gates land: %q %+v", x.WorkflowStatus, x.RewindStats)
	}
	// the third FAIL finds the one total budget spent: the call refuses it
	x := readExec(t, execPath)
	if why := RewindBudgetSpent(d.workflow, x); !strings.Contains(why, "2 of max_rewinds 2") {
		t.Fatalf("two rewinds across two gates spend max_rewinds 2: %q %+v", why, x.RewindStats)
	}
}

// max_rewinds has no per-edge bound: one gate may spend the whole budget.
func TestMaxRewindsHasNoEdgeBound(t *testing.T) {
	wf, err := LoadWorkflow(workflowtest.WriteLane(t, t.TempDir(), "one-edge", "max_rewinds: 3\n"+twoGateSteps))
	if err != nil {
		t.Fatal(err)
	}
	d, _, _, execPath := testEnv(t, wf.Steps...)
	d.workflow = wf
	mustInit(t, d)
	for n := int64(1); n <= 5; n += 2 {
		driveEdge(t, d, "build", "check-a", n)
	}
	x := readExec(t, execPath)
	if x.RewindStats.Count != 3 || x.WorkflowStatus != StatusRunning {
		t.Fatalf("three rewinds on one edge land under max_rewinds: 3: %+v", x.RewindStats)
	}
	for _, r := range x.RewindStats.Rewinds {
		if r.Class != capsule.RewindCharged {
			t.Fatalf("the ledger still classes each rewind: %+v", x.RewindStats.Rewinds)
		}
	}
}

// Every shipped lane with a rewind edge declares max_rewinds (the loader
// refuses one that does not, WFC-113).
func TestShippedLanesDeclareMaxRewinds(t *testing.T) {
	for bundle, want := range map[string]int{"micro": 3, "develop": 3} {
		wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, bundle))
		if err != nil {
			t.Fatalf("%s: %v", bundle, err)
		}
		if wf.MaxRewinds != want {
			t.Errorf("%s: max_rewinds %d, want %d", bundle, wf.MaxRewinds, want)
		}
	}
}
