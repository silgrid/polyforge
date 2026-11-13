package workflow_test

// c5_exclusive_test.go (workflow contract 5): the lane key `exclusive` —
// a boolean, absent = false, on a built-in lane and a user lane alike; any
// other value is WFC-112. Of the shipped lanes integrate alone declares it
// (one unfinished batch per project). The `integration: true` marker it
// replaces is gone: an unknown key, WFC-111, whatever its value, on a
// built-in lane and a user lane alike.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestC5ExclusiveLaneKey(t *testing.T) {
	for front, want := range map[string]bool{
		"exclusive: true\nsteps:\n  - id: work\n":  true,
		"exclusive: false\nsteps:\n  - id: work\n": false,
		"steps:\n  - id: work\n":                   false,
	} {
		lane, errs := workflow.Load(workflowtest.WriteLane(t, t.TempDir(), "demo", front))
		if len(errs) > 0 || lane.Exclusive != want {
			t.Fatalf("%q: exclusive %v, want %v (%v)", front, lane.Exclusive, want, errs)
		}
	}
	for _, v := range []string{`"true"`, "yes-please", "1", "[true]"} {
		refusedOnce(t, "exclusive: "+v+"\nsteps:\n  - id: work\n", workflow.CodeInvalidValue, "exclusive", "want true or false")
	}
	set := workflow.ForWorkspace(t.TempDir(), t.TempDir())
	workflowtest.WriteLane(t, set.User, "mine", "exclusive: true\nsteps:\n  - id: work\n")
	if lane, _, err := set.Load("mine"); err != nil || lane == nil || !lane.Exclusive {
		t.Fatalf("a user lane declares it too: %+v %v", lane, err)
	}
}

func TestC5ShippedIntegrateIsExclusive(t *testing.T) {
	for _, name := range []string{"develop", "integrate", "micro", "release"} {
		lane, errs := workflow.Load(filepath.Join(shippedLanes, name))
		if len(errs) > 0 {
			t.Fatalf("%s: %v", name, errs)
		}
		if lane.Exclusive != (name == "integrate") {
			t.Errorf("%s: exclusive %v — integrate alone declares it", name, lane.Exclusive)
		}
	}
}

func TestC5IntegrationMarkerIsAnUnknownKey(t *testing.T) {
	for _, v := range []string{"true", "{ fix_lane: builder }"} {
		e := refusedOnce(t, "integration: "+v+"\nsteps:\n  - id: work\n", workflow.CodeUnknownKey, "integration")
		if strings.Contains(e.Msg, "marker") {
			t.Errorf("no rewrite names the marker any more: %s", e.Msg)
		}
	}
	set := workflow.ForWorkspace(t.TempDir(), t.TempDir())
	workflowtest.WriteLaneUnchecked(t, set.User, "mine", "integration: true\nsteps:\n  - id: work\n")
	lanes, err := set.UserLanes()
	if err != nil || len(lanes) != 1 || len(lanes[0].Errors) != 1 || lanes[0].Errors[0].Code != workflow.CodeUnknownKey {
		t.Fatalf("a user lane carrying it: %+v %v", lanes, err)
	}
}
