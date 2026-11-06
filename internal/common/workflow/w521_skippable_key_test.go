package workflow_test

// w521_skippable_key_test.go: the step key `skippable` —
// a bool, absent = false — says a project may switch the step off
// (`steps.<project>.<step>.skip`); any other value is WFC-112 at the
// step's path. The registry dump and the generated lane Schema carry it,
// the Schema as a boolean. Contract 5 is extended in place (owner ruling
// 2026-11-12, as for): an optional key whose default is the
// present behaviour does not bump the contract version.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestW521StepSkippableKey(t *testing.T) {
	for front, want := range map[string]bool{
		"steps:\n  - id: x\n    skippable: true\n":  true,
		"steps:\n  - id: x\n    skippable: false\n": false,
		"steps:\n  - id: x\n":                       false,
	} {
		lane, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front))
		if len(errs) > 0 || lane == nil {
			t.Errorf("%q: defects %v, want none", front, errs)
			continue
		}
		if got := lane.Steps[0].Skippable; got != want {
			t.Errorf("%q: Skippable = %v, want %v", front, got, want)
		}
	}

	front := "steps:\n  - id: x\n    skippable: maybe\n"
	_, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front))
	if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "steps[0](x).skippable" {
		t.Errorf("%q: defects %v, want one %s at steps[0](x).skippable", front, errs, workflow.CodeInvalidValue)
	}

	dump := workflow.RegistryDump()
	if !strings.Contains(dump, "\nskippable: bool (default false)") {
		t.Errorf("the registry dump carries no `skippable: bool (default false)` step key:\n%s", dump)
	}
	raw, err := workflow.LaneSchema()
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]struct {
					Type string `json:"type"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if p, ok := schema.Properties["steps"].Items.Properties["skippable"]; !ok || p.Type != "boolean" {
		t.Errorf("the lane Schema's step skippable: %+v (present %v), want {type: boolean}", p, ok)
	}
}
