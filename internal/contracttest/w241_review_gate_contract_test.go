// carries: workflows/_shared/steps/review.md, workflows/develop/fragments/review-judge.md, workflows/_shared/skills/review.md, internal/contracttest/w241_review_gate_contract_test.go

package test

// w241_review_gate_contract_test.go (polyforge#241, AC3): review's step
// text, its judge fragment and its skill agree that the mechanical items
// the build's done gate already judges (`facts_proof`) are not, on their
// own, a blocking finding — review recomputes them and records a hit as
// non-blocking, naming the gate that should have refused it — and that a
// hit the gate's result did not carry (the gate was bypassed or its result
// forged) is blocking `guarantee`. The five blocking classes stay as they
// are. Whitespace-flattened, so a rewrap keeps the sentences pinned.

import (
	"strings"
	"testing"
)

func TestW241ReviewGateContract(t *testing.T) {
	texts := map[string][]string{
		"workflows/_shared/steps/review.md": {
			"**`bug`**",
			"**`ac_gap`**",
			"**`project_rule`**",
			"**`test_weakened`**",
			"**`guarantee`**",
		},
		"workflows/develop/fragments/review-judge.md": {},
		"workflows/_shared/skills/review.md":          {},
	}
	for rel, wants := range texts {
		flat := w68Flat(t, rel)
		// the gate-covered subset: non-blocking on its own, a bypass is a guarantee
		for _, want := range append(wants,
			"The hand-off gate's subset.",
			"not, on their own, a blocking finding here",
			"the hand-off gate should have refused this",
			"blocking `guarantee`, naming the item") {
			if !strings.Contains(flat, want) {
				t.Errorf("%s must state %q", rel, want)
			}
		}
	}
}
