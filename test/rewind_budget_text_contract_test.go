package test

// rewind_budget_text_contract_test.go: every lane's rewind-budget text
// names the owner-routed exemption (neither the edge slot nor max_total),
// and the scoped-capable texts state that 「全量重验」 is owner-routed too.

import (
	"strings"
	"testing"
)

func TestRewindBudgetTextContract(t *testing.T) {
	for _, lane := range []string{"feature-full", "bug-fix", "chore", "autopilot"} {
		text := w68Flat(t, "workflows/"+lane+"/workflow.md")
		for _, want := range []string{
			"counts every automatic rewind",
			"charges neither the edge slot nor `max_total`",
			"「全量重验」, which is owner-routed",
		} {
			if !strings.Contains(strings.ReplaceAll(text, "charge neither", "charges neither"), want) {
				t.Errorf("%s: the rewind budget text must state %q", lane, want)
			}
		}
	}
	deliver := w68Flat(t, "workflows/_shared/steps/deliver.md")
	if !strings.Contains(deliver, "Both rewinds are owner-routed: a deliver FAIL a human answered in this attempt charges neither the edge slot nor `max_total`") {
		t.Error("the deliver contract must state that its human-answered rewinds are owner-routed")
	}
}
