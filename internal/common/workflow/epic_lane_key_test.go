package workflow_test

// epic_lane_key_test.go (polyforge#117 u1, AC1): the lane key `epic` —
// a boolean, absent = false, on a built-in lane and a user lane alike —
// marks the lane's own work items epics: each works on the children
// branch of its own seq (the lane's `children_branch`, which an epic
// lane must declare) and merges into the origin's default branch. A
// lane that declares `epic: true` without `children_branch` is WFC-112
// at the key; any non-boolean value is WFC-112 the same way `exclusive`
// judges its own.
//
// Expected values are derived from the work item's own design (the
// owner-confirmed plan: a declared, name-free capability key), not from
// the implementation: the accepted lane is the shipped epic lane's
// shape in miniature, and each refused value names a rule the ticket
// states (epic needs children_branch; a boolean).

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// TestEpicLaneKey pins the load of a lane that declares `epic`: with
// children_branch it loads with no defect and Lane.Epic is true; a lane
// that declares none keeps the zero value (the key is optional); a user
// lane declares it too; and a non-boolean value is refused at the key.
func TestEpicLaneKey(t *testing.T) {
	dir := workflowtest.WriteLane(t, t.TempDir(), "demo",
		"children_branch: \"epic/{seq}\"\nepic: true\nresume_reset: fix\nsteps:\n  - { id: fix }\n")
	lane, errs := workflow.Load(dir)
	if len(errs) != 0 {
		t.Fatalf("a lane declaring epic over children_branch loads with no defect, got %v", errs)
	}
	if lane == nil || !lane.Epic {
		t.Fatalf("Lane.Epic = %v, want true", lane)
	}
	// the key is optional: a lane without it loads with the zero value
	plain, errs := workflow.Load(workflowtest.WriteLane(t, t.TempDir(), "demo",
		"children_branch: \"epic/{seq}\"\nsteps:\n  - { id: fix }\n"))
	if plain == nil || plain.Epic || len(errs) != 0 {
		t.Fatalf("a lane declaring no epic loads with the zero value, got %v, %v", plain, errs)
	}
	// a user lane declares it too, over its own children_branch
	set := workflow.ForWorkspace(t.TempDir(), t.TempDir())
	workflowtest.WriteLane(t, set.User, "mine",
		"children_branch: \"feature/{seq}\"\nepic: true\nresume_reset: fix\nsteps:\n  - { id: fix }\n")
	user, _, err := set.Load("mine")
	if err != nil || user == nil || !user.Epic {
		t.Fatalf("a user lane declares epic too: %v, %v", user, err)
	}
	// epic does not weaken children_branch's own judgement: a defective
	// template is refused at children_branch, exactly as without epic
	defect := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo",
		"children_branch: \"epic/{n}\"\nepic: true\nresume_reset: fix\nsteps:\n  - { id: fix }\n")
	_, errs = workflow.Load(defect)
	if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "children_branch" {
		t.Fatalf("defects %v, want one %s at children_branch", errs, workflow.CodeInvalidValue)
	}
	// a non-boolean value is WFC-112 at the key
	for _, v := range []string{`"true"`, "yes", "1", "[true]"} {
		bad := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo",
			"children_branch: \"epic/{seq}\"\nepic: "+v+"\nsteps:\n  - { id: fix }\n")
		_, errs := workflow.Load(bad)
		if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "epic" {
			t.Fatalf("epic: %q: defects %v, want one %s at epic", v, errs, workflow.CodeInvalidValue)
		}
		if !strings.Contains(errs[0].Msg, "true or false") {
			t.Errorf("epic: %q: the defect %q does not say what a boolean wants", v, errs[0].Msg)
		}
	}
}

// TestEpicShippedLaneLoads pins the SHIPPED epic lane's load: it loads
// strict with its capability keys and its rewind budget.
func TestEpicShippedLaneLoads(t *testing.T) {
	lane, errs := workflow.Load(filepath.Join(shippedLanes, "epic"))
	if len(errs) != 0 {
		t.Fatalf("the shipped epic lane loads with no defect, got %v", errs)
	}
	if lane == nil || !lane.Epic || lane.ChildrenBranch != "epic/{seq}" {
		t.Fatalf("the shipped epic lane declares epic over epic/{seq}: %+v", lane)
	}
	strict, err := workflow.LoadStrict(filepath.Join(shippedLanes, "epic"))
	if err != nil || strict == nil || strict.MaxRewinds != 3 {
		t.Fatalf("the shipped epic lane loads strict with max_rewinds 3: %v, %v", strict, err)
	}
}

// TestEpicResumeResetKey pins the lane key `resume_reset` (polyforge#117
// r4): the step an owner-resumed epic's changed-children marker resets
// the lane to — a lane declaring `epic` must name it (it is what the
// runtime writes into the marker and the driver rewinds to), a lane
// without `epic` refuses it, and it must name a step of this lane.
func TestEpicResumeResetKey(t *testing.T) {
	dir := workflowtest.WriteLane(t, t.TempDir(), "demo",
		"children_branch: \"epic/{seq}\"\nepic: true\nresume_reset: sync\nsteps:\n  - { id: verify-children }\n  - { id: sync }\n")
	lane, errs := workflow.Load(dir)
	if len(errs) != 0 {
		t.Fatalf("an epic lane naming its resume_reset step loads with no defect, got %v", errs)
	}
	if lane == nil || lane.ResumeReset != "sync" {
		t.Fatalf("Lane.ResumeReset = %v, want sync", lane)
	}
	// epic without the key is refused at the key
	bare := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo",
		"children_branch: \"epic/{seq}\"\nepic: true\nsteps:\n  - { id: sync }\n")
	_, errs = workflow.Load(bare)
	if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "epic" {
		t.Fatalf("epic without resume_reset: defects %v, want one %s at epic", errs, workflow.CodeInvalidValue)
	}
	if !strings.Contains(errs[0].Msg, "resume_reset") {
		t.Errorf("the defect %q does not name resume_reset", errs[0].Msg)
	}
	// the key without epic is refused at the key
	lonely := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo",
		"children_branch: \"epic/{seq}\"\nresume_reset: sync\nsteps:\n  - { id: sync }\n")
	_, errs = workflow.Load(lonely)
	if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "resume_reset" {
		t.Fatalf("resume_reset without epic: defects %v, want one %s at resume_reset", errs, workflow.CodeInvalidValue)
	}
	// a step this lane does not define is refused at the key
	missing := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo",
		"children_branch: \"epic/{seq}\"\nepic: true\nresume_reset: nope\nsteps:\n  - { id: sync }\n")
	_, errs = workflow.Load(missing)
	if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "resume_reset" {
		t.Fatalf("resume_reset naming no step of the lane: defects %v, want one %s at resume_reset", errs, workflow.CodeInvalidValue)
	}
}

// TestEpicLaneKeyNeedsChildrenBranch: `epic: true` names the branch its
// work items work on through the lane's children_branch — a lane that
// declares the key without the template is refused at the key naming
// children_branch (WFC-112); `epic: false` alone stays a plain lane.
func TestEpicLaneKeyNeedsChildrenBranch(t *testing.T) {
	dir := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo",
		"epic: true\nresume_reset: fix\nsteps:\n  - { id: fix }\n")
	_, errs := workflow.Load(dir)
	if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "epic" {
		t.Fatalf("defects %v, want one %s at epic", errs, workflow.CodeInvalidValue)
	}
	if !strings.Contains(errs[0].Msg, "children_branch") {
		t.Errorf("the defect %q does not name children_branch", errs[0].Msg)
	}
	// epic: false declares nothing: the lane stays plain
	plain, errs := workflow.Load(workflowtest.WriteLane(t, t.TempDir(), "demo",
		"epic: false\nsteps:\n  - { id: fix }\n"))
	if plain == nil || plain.Epic || len(errs) != 0 {
		t.Fatalf("epic: false loads as a plain lane, got %v, %v", plain, errs)
	}
}
