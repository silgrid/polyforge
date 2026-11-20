// carries: workflows/develop/steps/build.md, workflows/_shared/skills/build.md, workflows/develop/README.md, docs/concepts.md

package test

// w181_plan_analysis_contract_test.go (polyforge#181, AC2): the texts
// that state the plan's analysis-mapping duty carry its five elements —
// the plan carries a `## 分析对照` section of its own beside `## Units`
// (never inside it: the plan-conformance scan reads `## Units` up to
// the next `##` heading, so a section nested there would cut the units
// it parses), each item the ticket's pre-submit analysis lists — each
// old test and document obligation, each risk, each decided point
// (`已定`) — mapped to the unit that carries it or the stated reason it
// is not taken, a ticket without `## 分析` writing `none` there, a
// decided point taking precedence over the analysis's recommendation,
// and an analysis written against an older base having its file:line
// references re-checked against the current one. The develop step asks
// it of every plan it writes; the shared skill conditions it — a round
// that wrote the plan on a ticket carrying `## 分析` adds the section,
// a round that keeps an inherited plan adds nothing. Whitespace-
// flattened, so a rewrap keeps them pinned.

import (
	"strings"
	"testing"
)

func TestW181PlanMapsTheAnalysis(t *testing.T) {
	for rel, wants := range map[string][]string{
		"workflows/develop/steps/build.md": {
			// (5) the plan carries the section, outside `## Units`
			"The plan carries a `## 分析对照` section beside `## Units` — its own `##` heading, never inside it (the plan-conformance scan reads `## Units` up to the next `##` heading)",
			// (1) every item mapped, or the stated reason it is not taken
			"each item the analysis lists — each old test and document obligation, each risk, each decided point (`已定`) — is mapped to the unit that carries it, or the stated reason it is not taken",
			// (3) a decided point takes precedence over the analysis's recommendation
			"a decided point takes precedence over the analysis's recommendation",
			// (4) an older base's file:line references re-checked first
			"re-checked against the current base before the work starts",
			// (2) no analysis: the section says none
			"a ticket without `## 分析` writes `none` there",
		},
		"workflows/_shared/skills/build.md": {
			// the shared skill's condition: a round that wrote the plan adds the section
			"A round that wrote the plan adds its `## 分析对照` section beside `## Units` when the ticket's content carries a `## 分析` section",
			"each item the analysis lists — each old test and document obligation, each risk, each decided point (`已定`) — mapped to the unit that carries it, or the stated reason it is not taken",
			"a decided point takes precedence over the analysis's recommendation",
			"re-checked against the current base before the work starts",
			"a ticket without `## 分析` writes `none` there",
			// an inherited plan adds nothing (the fix build that never re-plans)
			"A round that keeps an inherited plan adds nothing to it",
		},
		"workflows/develop/README.md": {
			"its `## 分析对照` section mapping the ticket's `## 分析` items to units or stated reasons",
		},
		"docs/concepts.md": {
			"A plan written on a ticket that carries a `## 分析` section also carries a `## 分析对照` section mapping each item the analysis lists — each old test and document obligation, each risk, each decided point — to the unit that carries it, or the stated reason it is not taken",
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
