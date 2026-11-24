package workflow_test

// landing_lock_test.go (polyforge#245 245-lane): the lane key
// `landing_lock: {enter, exit}` — an optional closed step interval the
// daemon holds the server landing lease in. The key is optional (no
// interval = today's behaviour, the compatible extension the contract's
// version rule allows), both endpoints name steps of this lane in
// declaration order, and the registry dump, the generated lane Schema and
// the shipped develop/epic lanes carry it (sync..merge).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func landingFront(block string) string {
	return "max_rewinds: 1\n" + block + "steps:\n  - id: build\n  - id: sync\n  - id: merge\n    gate: true\n    on_fail: { target: build }\n"
}

func TestLandingLockDeclaration(t *testing.T) {
	// absent: no interval
	lane, errs := workflow.Load(workflowtest.WriteLane(t, t.TempDir(), "demo", landingFront("")))
	if len(errs) > 0 || lane == nil {
		t.Fatalf("no landing_lock: defects %v, want none", errs)
	}
	if lane.LeaseInterval != nil {
		t.Fatalf("no landing_lock: got %+v, want nil", lane.LeaseInterval)
	}

	// a declared interval loads with both endpoints
	lane, errs = workflow.Load(workflowtest.WriteLane(t, t.TempDir(), "demo",
		landingFront("landing_lock: { enter: sync, exit: merge }\n")))
	if len(errs) > 0 || lane == nil {
		t.Fatalf("landing_lock sync..merge: defects %v, want none", errs)
	}
	if lane.LeaseInterval == nil || lane.LeaseInterval.Enter != "sync" || lane.LeaseInterval.Exit != "merge" {
		t.Fatalf("landing_lock: %+v, want sync..merge", lane.LeaseInterval)
	}

	// a one-step interval is legal (enter == exit)
	lane, errs = workflow.Load(workflowtest.WriteLane(t, t.TempDir(), "demo",
		landingFront("landing_lock: { enter: merge, exit: merge }\n")))
	if len(errs) > 0 || lane == nil || lane.LeaseInterval == nil || lane.LeaseInterval.Enter != "merge" {
		t.Fatalf("landing_lock merge..merge: lane %+v defects %v, want the closed one-step interval", lane, errs)
	}

	// illegal declarations are refused at load: an unknown step, a
	// reversed range, a missing endpoint, an unknown sub-key
	for name, block := range map[string]string{
		"unknown enter": "landing_lock: { enter: review, exit: merge }\n",
		"reversed":      "landing_lock: { enter: merge, exit: sync }\n",
		"no exit":       "landing_lock: { enter: sync }\n",
		"extra key":     "landing_lock: { enter: sync, exit: merge, third: build }\n",
	} {
		_, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", landingFront(block)))
		if len(errs) == 0 {
			t.Errorf("%s: loaded with no defect, want one", name)
			continue
		}
	}
}

// TestLandingLockReversedRefused: a reversed interval is refused at load
// (the closed range names one order).
func TestLandingLockReversedRefused(t *testing.T) {
	_, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo",
		landingFront("landing_lock: { enter: merge, exit: sync }\n")))
	if len(errs) == 0 {
		t.Fatal("a reversed landing_lock interval loaded with no defect")
	}
}

// TestLandingLockCarriedEverywhere: the registry dump, the generated lane
// Schema and the two lanes the ticket names carry the one optional key.
func TestLandingLockCarriedEverywhere(t *testing.T) {
	if dump := workflow.RegistryDump(); !strings.Contains(dump, "\nlanding_lock: map {enter, exit}") {
		t.Errorf("the registry dump carries no landing_lock lane key:\n%s", dump)
	}
	raw, err := workflow.LaneSchema()
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema.Properties["landing_lock"]; !ok {
		t.Errorf("the lane Schema carries no landing_lock property")
	}
	for _, laneName := range []string{"develop", "epic"} {
		lane, errs := workflow.Load("../../../workflows/" + laneName)
		if len(errs) > 0 || lane == nil {
			t.Fatalf("%s: defects %v", laneName, errs)
		}
		if lane.LeaseInterval == nil || lane.LeaseInterval.Enter != "sync" || lane.LeaseInterval.Exit != "merge" {
			t.Errorf("%s: landing_lock = %+v, want sync..merge", laneName, lane.LeaseInterval)
		}
	}
}
