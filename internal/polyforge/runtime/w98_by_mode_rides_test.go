package runtime

// w98_by_mode_rides_test.go (polyforge#98): a lane's step may read the
// mode of a step the lane does not carry — the integration lane's full
// gate routes the batch's in-batch repair on the project's merge mode —
// so the claim's frozen step modes carry the project's by declarations
// for steps outside the lane unchanged (the claim still never refuses
// over one: the refusal stays only for a step the lane carries and does
// not declare human_mergeable, exactly as before).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// TestW98ByModeOfANonCarriedStepRidesTheFreeze: the project's by on a
// step no lane of this claim carries rides into the capsule's frozen
// step modes as declared, while a by on a step the lane carries without
// human_mergeable still refuses the claim (the r1 rule unchanged).
func TestW98ByModeOfANonCarriedStepRidesTheFreeze(t *testing.T) {
	r, ws, _ := newWorkspaceTestRuntime(t, newFakeHub(), "proj")
	lanes := t.TempDir()
	workflowtest.WriteLane(t, lanes, "demo-rides",
		"steps:\n  - { id: collect }\n  - { id: close }\n")
	r.cfg.WorkflowsDir = lanes
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    merge: { by: human }\n")
	capDir, err := w36Claim(t, r, "wi_w98rides", "demo-rides")
	if err != nil {
		t.Fatalf("a claim over a by naming a step the lane does not carry is never refused: %v", err)
	}
	if got := w36Modes(t, capDir); !reflect.DeepEqual(got, map[string]string{"merge": "human"}) {
		t.Fatalf("the by declaration rides into the frozen step modes unchanged: %v, want {merge: human}", got)
	}

	// the refusal rule is untouched: a by naming a step the lane carries
	// without human_mergeable still refuses the claim naming it
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    collect: { by: human }\n")
	if _, err := w36Claim(t, r, "wi_w98refused", "demo-rides"); err == nil ||
		!strings.Contains(err.Error(), "steps.proj.collect.by") {
		t.Fatalf("a by on a carried step the lane does not declare human_mergeable still refuses: %v", err)
	}
}
