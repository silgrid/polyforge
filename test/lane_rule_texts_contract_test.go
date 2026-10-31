package test

// lane_rule_texts_contract_test.go: the never-unattended removal item is
// narrowed to removals the work item does not name — filing a named
// removal on chore is the owner's ruling, a pure deletion still gets the
// chore down-lane proposal — and the weak-test rule applies only to tests
// that pin new or changed behaviour.

import (
	"strings"
	"testing"
)

func TestLaneRuleTextsContract(t *testing.T) {
	const narrowed = "removing features or weakening guarantees the work item does not name"
	grill := w68Flat(t, "workflows/feature-full/steps/grill.md")
	for _, want := range []string{narrowed, "A removal the work item names is the owner's ruling for that removal once it is filed on `chore`", "a pure-deletion ticket", "so it is a down-lane proposal too"} {
		if !strings.Contains(grill, want) {
			t.Errorf("feature-full grill must state %q", want)
		}
	}
	if !strings.Contains(w68Flat(t, "docs/concepts.md"), narrowed) {
		t.Error("the concepts page must narrow the never-unattended removal item the same way")
	}
	chore := w68Flat(t, "workflows/chore/workflow.md")
	if !strings.Contains(chore, "filing a removal the work item names on `chore` is the owner's ruling for that removal") ||
		!strings.Contains(chore, "a removal or guarantee weakening the work item does not name still blocks") {
		t.Error("the chore lane must state the filing-as-ruling rule and its limit")
	}
	review := w68Flat(t, "workflows/_shared/steps/review.md")
	for _, want := range []string{"a removal the work item names, filed on `chore`, is ruled", "A new test that claims to pin new or changed behaviour yet passes on the old code (a characterization test of existing behaviour is exempt)"} {
		if !strings.Contains(review, want) {
			t.Errorf("the review checklist must state %q", want)
		}
	}
	if !strings.Contains(w68Flat(t, "workflows/_shared/principles.md"), "New-behaviour tests fail on the old code") {
		t.Error("the shared principle must scope the red-first rule to new or changed behaviour")
	}
}
