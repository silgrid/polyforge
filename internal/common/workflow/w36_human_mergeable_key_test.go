package workflow_test

// w36_human_mergeable_key_test.go (u2): the step key
// `human_mergeable` — a bool, absent = false — says a project may declare
// the step's work done by a person (`steps.<project>.<step>.by: human`,
// polyforge#36: the develop lane's merge — the PRs are handed to the
// project's reviewer); any other value is WFC-112 at the step's path. The
// registry dump and the generated lane Schema carry it, the Schema as a
// boolean. Contract 1 is extended in place, exactly as `skippable` was
// (owner ruling 2026-11-12): an optional key whose default is the present
// behaviour does not bump the contract version.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestW36HumanMergeableKey(t *testing.T) {
	for front, want := range map[string]bool{
		"steps:\n  - id: x\n    human_mergeable: true\n":  true,
		"steps:\n  - id: x\n    human_mergeable: false\n": false,
		"steps:\n  - id: x\n":                              false,
	} {
		lane, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front))
		if len(errs) > 0 || lane == nil {
			t.Errorf("%q: defects %v, want none", front, errs)
			continue
		}
		if got := lane.Steps[0].HumanMergeable; got != want {
			t.Errorf("%q: HumanMergeable = %v, want %v", front, got, want)
		}
	}

	front := "steps:\n  - id: x\n    human_mergeable: maybe\n"
	_, errs := workflow.Load(workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", front))
	if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "steps[0](x).human_mergeable" {
		t.Errorf("%q: defects %v, want one %s at steps[0](x).human_mergeable", front, errs, workflow.CodeInvalidValue)
	}

	dump := workflow.RegistryDump()
	if !strings.Contains(dump, "\nhuman_mergeable: bool (default false)") {
		t.Errorf("the registry dump carries no `human_mergeable: bool (default false)` step key:\n%s", dump)
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
	if p, ok := schema.Properties["steps"].Items.Properties["human_mergeable"]; !ok || p.Type != "boolean" {
		t.Errorf("the lane Schema's step human_mergeable: %+v (present %v), want {type: boolean}", p, ok)
	}

	// the shipped develop lane declares its merge human_mergeable and no
	// other step, and micro declares none: the mode never reaches a lane
	// that does not ask for it
	for lane, want := range map[string][]string{"develop": {"merge"}, "micro": nil} {
		loaded, errs := workflow.Load(filepath.Join("..", "..", "..", "workflows", lane))
		if len(errs) > 0 || loaded == nil {
			t.Fatalf("the shipped %s lane: defects %v", lane, errs)
		}
		var got []string
		for _, s := range loaded.Steps {
			if s.HumanMergeable {
				got = append(got, s.ID)
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("the shipped %s lane's human_mergeable steps = %v, want %v", lane, got, want)
		}
	}
}
