package workflow_test

// w516_lane_acceptance_key_test.go: the lane key
// `acceptance` — `required` or `none`, absent = required — on a built-in
// lane and a user lane alike; any other value is WFC-112. The registry
// dump and the generated lane Schema carry it, the Schema as the enum
// none|required. Contract 5 is extended in place (owner ruling
// 2026-11-12): an optional key whose default is the present behaviour
// does not bump the contract version.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestW516LaneAcceptanceKey(t *testing.T) {
	for _, front := range []string{
		"acceptance: none\nsteps:\n  - id: work\n",
		"acceptance: required\nsteps:\n  - id: work\n",
		"steps:\n  - id: work\n",
	} {
		_, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front))
		if len(errs) > 0 {
			t.Errorf("%q: defects %v, want none", front, errs)
		}
	}
	for _, v := range []string{"maybe", "true"} {
		front := "acceptance: " + v + "\nsteps:\n  - id: work\n"
		_, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front))
		if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "acceptance" {
			t.Errorf("%q: defects %v, want one %s at acceptance", front, errs, workflow.CodeInvalidValue)
		}
	}

	set := workflow.ForWorkspace(t.TempDir(), t.TempDir())
	workflowtest.WriteLaneUnchecked(t, set.User, "mine", "acceptance: none\nsteps:\n  - id: work\n")
	if lane, _, err := set.Load("mine"); err != nil || lane == nil {
		t.Errorf("a user lane declares it too: %+v %v", lane, err)
	}

	if dump := workflow.RegistryDump(); !strings.Contains(dump, "\nacceptance: required|none") {
		t.Errorf("the registry dump carries no `acceptance: required|none` lane key:\n%s", dump)
	}
	raw, err := workflow.LaneSchema()
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if p, ok := schema.Properties["acceptance"]; !ok || !reflect.DeepEqual(p.Enum, []string{"none", "required"}) {
		t.Errorf("the lane Schema's acceptance: %+v (present %v), want the enum [none required]", p, ok)
	}
}
