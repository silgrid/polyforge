package test

// lane_rule_texts_contract_test.go: the never-unattended removal item is
// narrowed to removals the work item does not name, micro is a down-lane
// target only for an eligible pure text ticket, and the weak-test rule
// applies only to tests that pin new or changed behaviour. (The retired
// lanes' grill and chore texts it also pinned were removed.)

import (
	"strings"
	"testing"
)

func TestLaneRuleTextsContract(t *testing.T) {
	const narrowed = "removing features or weakening guarantees the work item does not name"
	// micro is a down-lane target only for an eligible,
	// opted-in pure text ticket, and the never-unattended clause is unchanged
	file := w68Flat(t, "skills/polyforge/verbs/file.md")
	for _, want := range []string{
		"| `micro` | none (no verification step",
		"may go down to `micro`; a ticket touching any repository without that declaration, or any path outside it, is never proposed `micro`",
		"the check never proposes a lighter lane for it — `micro` included",
		"makes the ticket ineligible for `micro`",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("the filing skill must state %q", want)
		}
	}
	if !strings.Contains(w68Flat(t, "docs/concepts.md"), narrowed) {
		t.Error("the concepts page must narrow the never-unattended removal item the same way")
	}
	review := w68Flat(t, "workflows/_shared/steps/review.md")
	for _, want := range []string{"A new test that claims to pin new or changed behaviour yet passes on the old code (a characterization test of existing behaviour is exempt)"} {
		if !strings.Contains(review, want) {
			t.Errorf("the review checklist must state %q", want)
		}
	}
	if !strings.Contains(w68Flat(t, "workflows/_shared/principles.md"), "New-behaviour tests fail on the old code") {
		t.Error("the shared principle must scope the red-first rule to new or changed behaviour")
	}
}
