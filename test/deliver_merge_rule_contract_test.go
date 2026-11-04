package test

// deliver_merge_rule_contract_test.go: the deliver contract states the
// merge rule's no-review carve-out, the already-merged acknowledge answer
// and the single follow-up-PR exception, each once.

import (
	"strings"
	"testing"
)

func TestDeliverMergeRuleContract(t *testing.T) {
	text := w68Flat(t, "workflows/_shared/steps/deliver.md")
	for _, want := range []string{
		"In a workflow without a `review` step (autopilot) there is no review head: verification's `verified_heads.<name>` stands in for this coverage",
		"- **acknowledge** - the human accepts the merge that happened",
		"This round's `acknowledge` answer settles it: never ask again",
		"open exactly one follow-up PR for those commits on the same branch - the only case a second PR is allowed",
		"an OPEN PR: reuse it (never a second PR)",
		// the verification-less lane's credential is this
		// dispatch's own in-window re-run of the recorded fast_lane plan
		"never through `pf bar-run`: a run of the build step or of an earlier dispatch never counts, only this dispatch's recorded run inside that window",
		"on a lane without a `verification` step the only cover is this dispatch's recorded re-run of every `fast_lane` command of the repo inside the window (step 3), each with exit 0",
	} {
		if n := strings.Count(text, want); n != 1 {
			t.Errorf("the deliver contract must state %q exactly once, found %d", want, n)
		}
	}
}
