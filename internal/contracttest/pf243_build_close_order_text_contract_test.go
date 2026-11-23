package test

// carries: workflows/develop/steps/build.md, workflows/_shared/skills/build.md, workflows/develop/README.md, internal/contracttest/pf243_build_close_order_text_contract_test.go

// pf243_build_close_order_text_contract_test.go (polyforge#243, AC2): the
// texts that state the build's close agree on three rules the ticket's
// owner rulings settled — the close's order is fixed (the scoped selection
// green, then the facts, then the self-review, then the hand-off), a close
// that changes no code does not re-run the scoped selection (the existing
// reuse key; artifacts alone are no code change) while a changed tree
// re-runs only itself and, on a host close, reuses the untouched roots by
// `--reuse` (a boxed close consumes none), and a red the scoped selection
// reports is taken from the result file's own baseline record, never
// reproduced by hand on the base worktree. The develop lane README's
// step-guard sentence names the build whole-suite block too. The negative
// cases (review r1#3) pin that no text states an unconditional never-re-run
// or a positive base reproduction. The sentences name no project, lane or
// concrete suite, the same neutrality the other build text contracts pin.
// Whitespace-flattened, so a rewrap keeps them pinned; the engine's own
// behaviour is pinned by the Python tests under workflows/_shared/scripts/tests/,
// not here.

import (
	"regexp"
	"strings"
	"testing"
)

// pf243NoNames: what a close-order text may not carry — a project, lane or
// concrete-suite identifier (a ticket reference included). The artifacts
// paths and the facts engine's own keys are the mechanism's names.
var pf243NoNames = regexp.MustCompile(`polyforge|pi-web|spec-superflow|e2e-parallel|develop lane|micro lane|#[0-9]`)

func TestPF243BuildCloseOrderText(t *testing.T) {
	order := "The close's order is fixed: the scoped selection green, then the facts, then the self-review, then the hand-off."
	rerun := "Once the close's scoped selection has run, a step that changes no code does not re-run it — the reuse key is the command text, the root's subtree tree hash and the declared prerequisites, so changing artifacts alone is not a code change; a step that does change code re-runs the selection only on the new tree, and on a host close the roots it did not touch are reused by `--reuse` (a boxed close consumes none)."
	red := "A red the scoped selection reports is reported from the result file's own baseline record (`details.baseline` and its `reds[]` classification); never reproduce it by hand on the base worktree."
	guard := "a whole-suite test command the repository's own declaration runs in its full phase is blocked with the written fast-check form and the runner's name filter"
	for _, rel := range []string{
		"workflows/develop/steps/build.md",
		"workflows/_shared/skills/build.md",
		"workflows/develop/README.md",
	} {
		flat := w68Flat(t, rel)
		for _, want := range []string{order, rerun, red} {
			if m := pf243NoNames.FindString(want); m != "" {
				t.Fatalf("a close-order rule must name no project, lane or concrete suite: %q carries %q", want, m)
			}
			if !strings.Contains(flat, want) {
				t.Errorf("%s must state %q", rel, want)
			}
		}
		// The negative cases (review r1#3): an unconditional never-re-run or
		// a positive "reproduce … on the base" requirement must fail the
		// contract. The known red sentence is removed first so its own
		// "never reproduce it by hand on the base worktree" does not match.
		stripped := strings.ReplaceAll(flat, red, "")
		for _, forbidden := range []*regexp.Regexp{
			regexp.MustCompile(`(?i)\b(?:never|do not|don't)\s+re-?runs?\b`),
			regexp.MustCompile(`(?i)\breproduce\b[^.]*\bon the base\b`),
		} {
			if m := forbidden.FindString(stripped); m != "" {
				t.Errorf("%s must not state %q: a close re-runs only on the new tree and reports a red from the result file, never by reproducing it on the base", rel, m)
			}
		}
	}
	// the develop lane README's step-guard sentence names the build rule too
	// (review r1#4): the guard description there is not stale.
	readme := w68Flat(t, "workflows/develop/README.md")
	if !strings.Contains(readme, guard) {
		t.Errorf("workflows/develop/README.md's step-guard sentence must state %q", guard)
	}
}