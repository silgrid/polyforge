package test

// e2e_mode_anchor_contract_test.go: the e2e-author mode's audit anchor is
// artifacts/grill-qa.md alone, and an all-green baseline offers the three
// owner answers; the skip is the named discipline-① exception, stated in
// the contributor guide.

import (
	"strings"
	"testing"
)

func TestE2EModeAnchorContract(t *testing.T) {
	lane := w68Flat(t, "workflows/feature-full/workflow.md")
	if n := strings.Count(lane, "the audit anchor is `artifacts/grill-qa.md` alone"); n != 1 {
		t.Errorf("the lane must name the one audit anchor exactly once, found %d", n)
	}
	if strings.Contains(lane, "`plan` states it — the audit anchor") {
		t.Error("plan's statement must not be the audit anchor")
	}
	for _, p := range []string{"workflows/feature-full/workflow.md", "workflows/feature-full/steps/e2e-author.md"} {
		text := w68Flat(t, p)
		for _, want := range []string{"`rewind-to-spec: <words>`", "`skip: <rationale>`", "`cancel`"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s must offer %s on an all-green baseline", p, want)
			}
		}
	}
	author := w68Flat(t, "workflows/feature-full/steps/e2e-author.md")
	for _, want := range []string{"every row then says `pass`", "append the owner's rationale verbatim to `artifacts/grill-qa.md`"} {
		if !strings.Contains(author, want) {
			t.Errorf("e2e-author must state %q", want)
		}
	}
	// The all-green run's commit never outlives the owner's answer: the
	// skip and the run after rewind-to-spec both revert it first, so no
	// authored file is left undeclared in the merge-base diff.
	for _, want := range []string{
		"`skip`: first revert the all-green run's own authoring commit",
		"the owner's `skip`, or the fresh run after `rewind-to-spec` — starts by reverting that commit",
		"`git revert --no-edit <sha>`",
		"a file an earlier run committed and this run does not list is an orphan",
	} {
		if !strings.Contains(author, want) {
			t.Errorf("e2e-author must state %q", want)
		}
	}
	if !strings.Contains(w68Flat(t, "docs/contributing.md"), "Named exception — behaviour already on main") {
		t.Error("discipline ① must name the behaviour-already-on-main exception")
	}
}
