package workflow_test

// rewind_only_key_test.go (polyforge#116 u1): the step key `rewind_only` —
// a bool, absent = false — says the step runs only after a rewind covers
// it; a covered declaration loads clean, `skippable` may sit beside it
// (the project skip wins, the driver's tests pin that), any other value
// is WFC-112 at the step's path, and the registry dump and the generated
// lane Schema carry the key, the Schema as a boolean. Contract 1 is
// extended in place (the skippable ruling of 2026-11-12): an optional
// key whose default is the present behaviour does not bump the version.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// rewindOnlyFront answers a lane whose step x declares rewind_only on a
// covered position (before the gate whose on_fail names build).
func rewindOnlyFront(value string) string {
	if value == "" {
		return "max_rewinds: 1\nsteps:\n  - id: x\n  - id: build\n  - id: check\n    gate: true\n    on_fail: { target: build }\n"
	}
	return fmt.Sprintf("max_rewinds: 1\nsteps:\n  - id: x\n    rewind_only: %s\n  - id: build\n  - id: check\n    gate: true\n    on_fail: { target: build }\n", value)
}

func TestRewindOnlyKeyParses(t *testing.T) {
	for front, want := range map[string]bool{
		rewindOnlyFront("true"):  true,
		rewindOnlyFront("false"): false,
		rewindOnlyFront(""):      false,
	} {
		lane, errs := workflow.Load(workflowtest.WriteLane(t, t.TempDir(), "demo", front))
		if len(errs) > 0 || lane == nil {
			t.Errorf("%q: defects %v, want none", front, errs)
			continue
		}
		if got := lane.Steps[0].RewindOnly; got != want {
			t.Errorf("%q: RewindOnly = %v, want %v", front, got, want)
		}
	}

	// `skippable` and `rewind_only` may sit on one step (the project
	// skip wins — the driver's tests pin the order).
	lane, errs := workflow.Load(workflowtest.WriteLane(t, t.TempDir(), "demo", "max_rewinds: 1\nsteps:\n  - id: x\n    skippable: true\n    rewind_only: true\n  - id: build\n  - id: check\n    gate: true\n    on_fail: { target: build }\n"))
	if len(errs) > 0 || lane == nil {
		t.Errorf("skippable beside rewind_only: defects %v, want none", errs)
	} else if !lane.Steps[0].Skippable || !lane.Steps[0].RewindOnly {
		t.Errorf("skippable+rewind_only: Skippable %v RewindOnly %v, want both true", lane.Steps[0].Skippable, lane.Steps[0].RewindOnly)
	}

	// any non-bool value is WFC-112 at the step's own path
	_, errs = workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", "max_rewinds: 1\nsteps:\n  - id: x\n    rewind_only: maybe\n  - id: build\n  - id: check\n    gate: true\n    on_fail: { target: build }\n"))
	if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "steps[0](x).rewind_only" {
		t.Errorf("rewind_only: maybe: defects %v, want one %s at steps[0](x).rewind_only", errs, workflow.CodeInvalidValue)
	}

	dump := workflow.RegistryDump()
	if !strings.Contains(dump, "\nrewind_only: bool (default false)") {
		t.Errorf("the registry dump carries no `rewind_only: bool (default false)` step key:\n%s", dump)
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
	if p, ok := schema.Properties["steps"].Items.Properties["rewind_only"]; !ok || p.Type != "boolean" {
		t.Errorf("the lane Schema's step rewind_only: %+v (present %v), want {type: boolean}", p, ok)
	}
}
