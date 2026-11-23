// carries: workflows/_shared/steps/review.md, workflows/develop/fragments/review-judge.md, workflows/_shared/skills/review.md, docs/concepts.md, workflows/develop/README.md, internal/contracttest/w241_review_gate_contract_test.go

package test

// w241_review_gate_contract_test.go (polyforge#241, AC3): review's step
// text, its judge fragment and its skill agree that the mechanical items
// the build's done gate already judges (`facts_proof`) are not, on their
// own, a blocking finding — review recomputes them and records a hit as
// non-blocking, naming the gate that should have refused it — and that a
// hit the gate's result did not carry (the gate was bypassed or its result
// forged) is blocking `guarantee`. The five blocking classes stay as they
// are. The negative half is the ticket's own counterexample: a text that
// writes a gate-covered item as blocking on its own turns this test red,
// because the old sentences are named and refused. Whitespace-flattened, so
// a rewrap keeps the sentences pinned.

import (
	"strings"
	"testing"
)

func TestW241ReviewGateContract(t *testing.T) {
	subset := map[string][]string{
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
	for rel, wants := range subset {
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

	// The semantic items stay blocking on their own (the gate judges none of
	// them): the judge fragment states it in so many words (polyforge#241
	// review r2#1 — "So is …" must not put them in the subset).
	judge := w68Flat(t, "workflows/develop/fragments/review-judge.md")
	if !strings.Contains(judge, strings.Join(strings.Fields(
		"A deleted or relaxed assertion, a changed helper or fixture the test relies on, or an input changed so the assertion holds trivially, with no finding cited for it in build's 关键决定和理由, is blocking `test_weakened`"), " ")) {
		t.Errorf("the judge fragment must keep the semantic test_weakened items blocking on their own")
	}
	// the skill's pre_existing_red sentence follows the same subset (N12)
	skill := w68Flat(t, "workflows/_shared/skills/review.md")
	if !strings.Contains(skill, strings.Join(strings.Fields(
		"a replay that exited 0 there (the test was green before the change, so it was not already red) is in the same subset"), " ")) {
		t.Errorf("the skill's pre_existing_red replay sentence must follow the gate subset")
	}

	// the two documents that describe review's behaviour state the same subset
	for rel, wants := range map[string][]string{
		"docs/concepts.md": {
			"in the hand-off gate's subset",
			"blocking `guarantee`",
		},
		"workflows/develop/README.md": {
			"in the hand-off gate's subset",
			"blocking `guarantee`",
		},
	} {
		flat := w68Flat(t, rel)
		for _, want := range wants {
			if !strings.Contains(flat, want) {
				t.Errorf("%s must state %q", rel, want)
			}
		}
	}

	// The old wording — a gate-covered item written as blocking on its own
	// — is refused: the ticket's counterexample ("write any of D1–D6 as
	// 'blocking … on its own' and the contract test fails").
	stale := map[string][]string{
		"workflows/develop/fragments/review-judge.md": {
			"or whose replay exited 0 — a test that passes on the old code, never red or weakened since — is blocking `test_weakened`",
			"a replay that exited 0 there (the test was green before the change) is blocking `test_weakened`",
			"neither red nor a pass: blocking `test_weakened`, naming the unit and its `rule`",
			"is a blocking `test_weakened` finding naming the entry",
			"So is a deleted or relaxed assertion, a new skip, a changed `carries:` header, a changed helper or fixture the test relies on, or an input changed so the assertion holds trivially, with no finding cited for it in build's 关键决定和理由.",
		},
		"workflows/_shared/skills/review.md": {
			"The resolution and commits facts are static — the same ones build's close ran without the replay (`facts.py build`) — and they block in every round",
			"each `problems` entry is blocking `test_weakened`, in every round",
			"is blocking `test_weakened` too",
		},
		"docs/concepts.md": {
			"its red evidence is missing, `test_weakened` too",
			"a red entry is a blocking `test_weakened` finding naming it",
		},
		"workflows/develop/README.md": {
			"every entry it fails is a blocking `test_weakened` finding naming the entry",
		},
	}
	for rel, olds := range stale {
		flat := w68Flat(t, rel)
		for _, old := range olds {
			if strings.Contains(flat, strings.Join(strings.Fields(old), " ")) {
				t.Errorf("%s still writes a gate-covered item as blocking on its own: %q", rel, old)
			}
		}
	}
}
