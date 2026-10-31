package test

// review_target_class_contract_test.go: the review contract declares the
// repair-target class once, with its closed enum, and the re-entry
// fragment makes a residual re-emit it.

import (
	"strings"
	"testing"
)

func TestReviewTargetClassContract(t *testing.T) {
	review := w68Flat(t, "workflows/_shared/steps/review.md")
	const enum = "enum: [code, authored-suite, spec, declared-obligation, instruction]"
	if n := strings.Count(review, enum); n != 1 {
		t.Errorf("the review schema must declare the repair_target enum exactly once, found %d", n)
	}
	for _, want := range []string{"repair_target:", "unfilled result, rejected", "Off-graph:", "amend-upstream / waive / cancel"} {
		if !strings.Contains(review, want) {
			t.Errorf("the review contract must state %q", want)
		}
	}
	// An obligation gap the build step can close itself (update the
	// unchanged doc, withdraw a moot obligation, cite an undeclared file)
	// is `code`; only a wrong obligation is off-graph, so such a gap never
	// interrupts the owner.
	for _, want := range []string{
		"every repair it can make in its own round is `code`, including updating a declared obligation the diff left unchanged, withdrawing a moot one in `withdrawn_obligations` and citing an undeclared file",
		"`declared-obligation` (reserved for a plan-declared obligation that is itself wrong and that {{build_step}} can neither satisfy nor withdraw)",
	} {
		if !strings.Contains(review, want) {
			t.Errorf("the repair_target description must state %q", want)
		}
	}
	if strings.Contains(review, "`declared-obligation` (a plan-declared coupled obligation)") {
		t.Error("the repair_target description must not define declared-obligation as any declared obligation")
	}
	const restated = "a declared obligation that is itself wrong or an un-IDed instruction — never `code`, which covers every repair the build step can make in its own round: updating a declared obligation the diff left unchanged, withdrawing a moot one, citing an undeclared file"
	for _, lane := range []string{"feature-full", "bug-fix", "chore"} {
		if !strings.Contains(w68Flat(t, "workflows/"+lane+"/workflow.md"), restated) {
			t.Errorf("the %s lane must restate the classes with the code-covers-every-own-round-repair rule", lane)
		}
	}
	if !strings.Contains(w68Flat(t, "docs/concepts.md"), "a declared obligation that is itself wrong or an un-IDed instruction, never `code`, which covers every repair the build step can make in its own round") {
		t.Error("docs/concepts.md must restate the classes with the code-covers-every-own-round-repair rule")
	}
	reentry := w68Flat(t, "workflows/_shared/review_reentry.md")
	if !strings.Contains(reentry, "A residual re-emits the original's `repair_target` class") {
		t.Error("the review re-entry fragment must make a residual re-emit its repair_target class")
	}
}
