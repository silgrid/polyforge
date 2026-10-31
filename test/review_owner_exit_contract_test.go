package test

// review_owner_exit_contract_test.go: the rendered review contracts state
// the owner exit's three answers and its one ruling landing point, and
// the authoring steps state the append-only amendment duty.

import (
	"strings"
	"testing"
)

func TestReviewOwnerExitContract(t *testing.T) {
	for _, lane := range []string{"feature-full", "bug-fix", "chore"} {
		text := w68Flat(t, "workflows/"+lane+"/workflow.md")
		for _, want := range []string{"the review owner exit", "`amend-upstream: <words>`", "`waive <refs> — <words>`", "or `cancel`", "`artifacts/grill-qa.md` `## Owner rulings`", "spec and plan are never re-run"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the lane text must state %q", lane, want)
			}
		}
	}
	if text := w68Flat(t, "workflows/autopilot/workflow.md"); strings.Contains(text, "review owner exit") {
		t.Error("autopilot has no review step and therefore no owner exit")
	}
	reentry := w68Flat(t, "workflows/_shared/review_reentry.md")
	for _, want := range []string{"`## Owner rulings`", "clears exactly the findings it names", "stays a residual", "No other location records a ruling."} {
		if !strings.Contains(reentry, want) {
			t.Errorf("review re-entry must state %q", want)
		}
	}
	review := w68Flat(t, "workflows/_shared/steps/review.md")
	if !strings.Contains(review, "amend-upstream / waive / cancel") {
		t.Error("the review schema must name the exit answers")
	}
	for _, p := range []string{"workflows/feature-full/steps/e2e-author.md", "workflows/bug-fix/steps/repro.md"} {
		if text := w68Flat(t, p); !strings.Contains(text, "append-only") || !strings.Contains(text, "byte-identical") {
			t.Errorf("%s must state the append-only amendment duty", p)
		}
	}
}
