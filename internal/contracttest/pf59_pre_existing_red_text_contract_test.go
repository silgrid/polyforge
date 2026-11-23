package test

// carries: workflows/_shared/skills/build.md, workflows/_shared/skills/review.md, workflows/_shared/steps/review.md, workflows/develop/steps/build.md, workflows/develop/fragments/review-judge.md, workflows/develop/README.md, workflows/_shared/build_subagents.md, internal/contracttest/pf59_pre_existing_red_text_contract_test.go

// pf59_pre_existing_red_text_contract_test.go: text contract for the
// units.json shape polyforge#59 adds — a unit whose reproduction test
// already exists and is red before the change declares
// `pre_existing_red: true` and reports no `test_commit`, and the facts
// engine replays its run on the tree of the change's base, red there
// being its red evidence. Every text that states how a build records such
// a unit, and how a review reads its replay (the build and review skills,
// the develop build step, the judgment fragment, the lane README, the
// sub-agent brief and the shared review step) agrees on the shape, when
// it is used and what its red evidence is. Whitespace-flattened, so a
// rewrap keeps them pinned; the engine's own behavior is pinned by
// facts.py's tests, not here.

import (
	"strings"
	"testing"
)

func TestPF59PreExistingRedTextContract(t *testing.T) {
	for rel, wants := range map[string][]string{
		"workflows/_shared/skills/build.md": {
			// when it is used: the reproduction test exists and is red before the change
			"A unit whose reproduction test already exists and is red before the change — an integration fix ticket: the test came with the batch, only the fix is this change — is not rewritten: it declares `\"pre_existing_red\": true` and reports no `test_commit`",
			// the red evidence is the engine's, taken from a replay on the change's base
			"The facts engine replays its run on the tree of that base and takes its red evidence from there",
		},
		"workflows/_shared/skills/review.md": {
			"A unit declared `pre_existing_red` (its reproduction test already exists and is red before the change) carries no `test_commit`: its `replay` runs on the tree of the change's base, and red there is its red evidence",
			"a replay that exited 0 there (the test was green before the change, so it was not already red) is in the same subset",
		},
		"workflows/develop/steps/build.md": {
			"A reproduction test that already exists and is red before the change is not rewritten: the unit declares `pre_existing_red` and reports no `test_commit`",
		},
		"workflows/develop/fragments/review-judge.md": {
			"A unit declared `pre_existing_red` (its reproduction test already exists and is red before the change) has no `test_commit`: its replay runs on the tree of the change's base, and red there is its red evidence",
			"a replay that exited 0 there (the test was green before the change) is in the same subset — the unit has no red evidence",
		},
		"workflows/develop/README.md": {
			"a unit whose reproduction test already exists red before the change declares `pre_existing_red: true` and carries no `test_commit`",
			"each unit's run replayed on the tree of its test commit (or of the change's base for a unit declared `pre_existing_red`, whose red evidence that replay is)",
		},
		"workflows/_shared/build_subagents.md": {
			"a unit declared `pre_existing_red` returns no `test_commit` — its reproduction test predates the change and its red evidence is the engine's base replay",
		},
		"workflows/_shared/steps/review.md": {
			"the units' red runs replayed at their test commits, or at the change's base for a unit declared `pre_existing_red`, included",
		},
	} {
		flat := w68Flat(t, rel)
		for _, w := range wants {
			if !strings.Contains(flat, w) {
				t.Errorf("%s must state %q", rel, w)
			}
		}
	}
}
