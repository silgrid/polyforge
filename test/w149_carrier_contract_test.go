package test

// w149_carrier_contract_test.go: text contract for — a plan
// never promises a deliverable no step contract carries.
//
// The duty lives once, in the shared fragment
// workflows/_shared/deliverable_carrier.md, and both plan steps include it:
// each rendered plan definition carries the duty's operative lines (the
// carrier section, the stated exclusion, the plan-carrier owner question,
// where the step contracts are read), no raw plan file carries a second
// copy, and the design docs name the fragment beside the other shared
// fragments. Red on the pre-change tree: the fragment does not exist and
// neither plan step includes it.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

var w149CarrierPlanSteps = []string{"chore", "feature-full"}

var w149CarrierDutyLines = []string{
	"## Deliverables and carriers",
	"## Excluded (no carrier step)",
	"no step contract carries it",
	"`question_id` `plan-carrier`",
	"`workflows/<bundle>/workflow.md`",
}

var w149CarrierPlaceholder = regexp.MustCompile(`\{\{[^}]*\}\}`)

func w149CarrierRead(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

func TestW149CarrierPlanStepsIncludeTheDuty(t *testing.T) {
	frag := strings.TrimSpace(w149CarrierRead(t, "workflows/_shared/deliverable_carrier.md"))
	if w149CarrierPlaceholder.MatchString(frag) {
		t.Errorf("the deliverable_carrier fragment carries a placeholder literal; a fragment is rendered verbatim")
	}
	for _, line := range w149CarrierDutyLines {
		if !strings.Contains(frag, line) {
			t.Errorf("the deliverable_carrier fragment lost its operative line %q", line)
		}
	}
	for _, bundle := range w149CarrierPlanSteps {
		rel := "workflows/" + bundle + "/steps/plan.md"
		raw := w149CarrierRead(t, rel)
		if strings.Count(raw, "{{deliverable_carrier}}") != 1 {
			t.Errorf("%s does not include {{deliverable_carrier}} exactly once", rel)
		}
		if strings.Contains(raw, frag) {
			t.Errorf("%s carries a second copy of the deliverable_carrier fragment", rel)
		}
		out, err := workflow.ReadStepDefinition(filepath.Join(v1Root(t), "workflows", bundle), "plan", nil)
		if err != nil {
			t.Fatalf("render %s: %v", rel, err)
		}
		def := string(out)
		if m := w149CarrierPlaceholder.FindString(def); m != "" {
			t.Errorf("%s renders with an unresolved placeholder %s", rel, m)
		}
		for _, line := range w149CarrierDutyLines {
			if strings.Count(def, line) != 1 {
				t.Errorf("%s renders the duty line %q %d times, want exactly 1", rel, line, strings.Count(def, line))
			}
		}
	}
}

func TestW149CarrierDesignDocsNameTheFragment(t *testing.T) {
	for _, rel := range []string{
		"docs/design/polyforge-vnext/01-workflow-skills.md",
		"docs/design/polyforge-vnext/reference.md",
	} {
		if !strings.Contains(w149CarrierRead(t, rel), "deliverable_carrier") {
			t.Errorf("%s does not name the deliverable_carrier shared fragment", rel)
		}
	}
}
