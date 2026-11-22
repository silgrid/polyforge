// carries: workflows/develop/*, workflows/develop/fragments/*, workflows/_shared/steps/*, docs/concepts.md, docs/contributing.md, skills/polyforge/*

package test

// w374_develop_driver_contract_test.go (D2; rewritten by
// workflow contract v2 PR 12): the texts that state how the develop
// lane's build is held to its contract agree with what holds it now — no
// daemon check: build runs its units red then green itself and records
// them in artifacts/build/units.json; review runs the pre-written
// facts.py lines (the units' red runs replayed at their test commits) and
// classes their facts under the schema's enum; verification runs the
// units again (verify.py --unit-runs); no sweep; the stops are build's own
// `pf step ask`s (workflow contract 4; human-only in the routing skill),
// and a review FAIL resumes build's previous session. Whitespace-flattened, so a
// rewrap keeps them pinned; build.md stays within its line budget — 120
// lines, raised to 145 when the grill and e2e authoring moved into build
// (A1), to 160 when the analysis and a big plan's units
// moved to sub-agents, to 170 when the close's two facts lines moved
// into the step (the static checks review judges with, run by the builder
// first; polyforge#131), and to 190 when the close's scoped selection
// moved in (polyforge#164), and to 215 when the close took review's own
// lines — the replay and plan-conformance — and the plan gained its
// analysis-mapping section (polyforge#181), and to 222 when the plan
// gained its decided-rulings mapping section (polyforge#221).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW374DevelopDriverChecksContract(t *testing.T) {
	root := v1Root(t)
	for rel, wants := range map[string][]string{
		"workflows/develop/steps/build.md": {
			"Your skill is what you read before you start, how you ask the owner, how a round a FAIL opened must change the tree, how each unit's run is recorded in `artifacts/build/units.json`, and what your hand-off carries.",
			"(5) record the unit in `artifacts/build/units.json` as your skill says — the run exactly as it ran red and green, and its marker.",
			// the build close's scoped selection (polyforge#164): the same
			// light-tier selection verification plans, run over the final
			// tree before the hand-off; its record lands in the change
			// analysis, and a red or incomplete result never hands off done
			"the build close's scoped selection — the same light-tier selection verification plans, one shared implementation, over the final tree",
			"`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/fast_checks.py\" --repo <name> --close-scoped --unit-runs artifacts/build/units.json --reuse artifacts/build/fast-checks/<repo>.json --json --out artifacts/build/scoped-close/<repo>.json`",
			"A red or `incomplete` result (the engine answered full, or a selected command cannot run) never hands off `done`",
			"for a work item whose verification runs the light tier (an integration batch's fix ticket runs the full bar and closes without one)",
			"Merge each touched repo's `details.selected` of its green or pre-existing result into the change analysis's `selected`",
		},
		"workflows/_shared/skills/build.md": {
			"`review` replays each unit's run on the tree of its test commit (`facts.py build --replay`: it must be red there) and `verification` runs it again on the final tree (`verify.py --unit-runs`: it must be green)",
			"Commit the unit's test files alone (`test_commit`) and run its targeted command on that clean commit yourself: it fails on an assertion.",
		},
		"workflows/_shared/skills/review.md": {
			"`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/facts.py\" plan-conformance --json --out artifacts/facts/plan-conformance.json`",
			"`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/facts.py\" build --replay --isolation host --json --out artifacts/facts/build.json`",
			"`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/facts.py\" carries --json --out artifacts/facts/carries.json`",
		},
		"workflows/develop/README.md": {
			"No sweep runs after a review PASS: its non-blocking findings, text-only ones included, reach the owner in the PR body.",
		},
		"workflows/develop/fragments/review-judge.md": {
			"`artifacts/facts/build.json` (`facts.py build --replay`) gives per unit its commits' test-first shape",
		},
		"workflows/_shared/steps/review.md": {
			"A FAIL sends the lane back to `{{rework_step}}`, which resumes its previous session and reads your section first.",
		},
		"docs/concepts.md": {
			"No daemon check judges what `build` reports",
			"(build asks with `pf step ask` before its first test, and review blocks a build whose first test commit is older than the owner's reply)",
		},
		"docs/contributing.md": {
			"No daemon check settles `build`",
			"Verification runs each unit's recorded run again on the final tree (`verify.py --unit-runs`).",
		},
		"skills/polyforge/SKILL.md": {
			"as are a develop build's stops (its `pf step ask` after the plan: the owner answers in free text that the build reads)",
		},
		"skills/polyforge/verbs/execute-intent.md": {
			"and so are a develop build's stops (its `pf step ask` after the plan: the owner answers in free text that the build reads)",
		},
		"skills/polyforge/verbs/file.md": {
			"never down-laned** off a lane that always keeps a human in the loop — `develop` carrying `confirm_plan: true` (build asks before its first test, and review blocks a build that did not) —",
			"The stops are human-only",
		},
	} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		flat := strings.Join(strings.Fields(string(raw)), " ")
		for _, w := range wants {
			if !strings.Contains(flat, w) {
				t.Errorf("%s must state %q", rel, w)
			}
		}
		for _, stale := range []string{"driver support: D2", "driver enforcement:", "driver enforcement arrives with D2", "the driver requires it there too until D2",
			// workflow contract v2 PR 12: the daemon judges no build content
			"Both runs MUST go through `pf bar-run`", "the driver lists each such hunk under `# Build facts`", "the driver rejects a sweep diff", "the driver enforces that stop"} {
			if strings.Contains(flat, stale) {
				t.Errorf("%s still promises %q: the driver does it now", rel, stale)
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "workflows/develop/steps/build.md"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "\n"); n > 222 {
		t.Errorf("workflows/develop/steps/build.md is %d lines, over its 222-line budget", n)
	}
}
