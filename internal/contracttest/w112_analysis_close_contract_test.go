// carries: workflows/develop/*, workflows/_shared/skills/*, workflows/develop/fragments/*, docs/concepts.md, docs/contributing.md, docs/reference/commands.md, workflows/_shared/scripts/facts.py
package test

// w112_analysis_close_contract_test.go (polyforge#112, AC5): the texts
// that state the change analysis's close check carry what the code now
// keeps — build's close runs facts.py analysis (the one shared
// validator verification's --analysis check calls) and a red must be
// fixed before the hand-off; review runs the same fact as its fourth
// facts line and a red analysis fact is always blocking (the lane goes
// back to build), never classed away; the references name the kind.
// Whitespace-flattened, so a rewrap keeps them pinned.

import (
	"strings"
	"testing"
)

func TestW112AnalysisCloseTextContract(t *testing.T) {
	const line = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" analysis --json --out artifacts/facts/analysis.json`
	for rel, wants := range map[string][]string{
		"workflows/develop/steps/build.md": {
			line,
			"the same validator `verification` calls",
			"fix it and re-run until green — never hand off with this check red",
		},
		"workflows/_shared/skills/build.md": {
			"commands: [fast_checks.py, facts.py]",
			"the same shared validator `verification` calls",
			"the close analysis check green",
			"`artifacts/facts/analysis.json`",
		},
		"workflows/_shared/skills/review.md": {
			line,
			"This fact red is never classed away: the lane goes back to `build`",
			"naming the entry and the rule it violates",
			"`artifacts/facts/analysis.json`",
		},
		"workflows/develop/fragments/review-judge.md": {
			"`artifacts/facts/analysis.json` (`facts.py analysis`)",
			"a blocking `test_weakened` finding naming the entry",
		},
		"workflows/develop/README.md": {
			"the facts of four pre-written lines",
			"must be fixed before the hand-off",
		},
		"docs/concepts.md": {
			"four pre-written lines",
			"the same shared validator",
		},
		"docs/contributing.md": {
			"`analysis` (the change analysis's own close check)",
		},
		"docs/reference/commands.md": {
			"the same shared validator verification's `--analysis` check calls",
		},
		"workflows/_shared/scripts/facts.py": {
			"analysis — the build close's own check of the change analysis",
		},
	} {
		flat := w68Flat(t, rel)
		for _, w := range wants {
			if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
				t.Errorf("%s must state %q", rel, w)
			}
		}
	}
}
