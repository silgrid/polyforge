// carries: workflows/_shared/skills/build.md, workflows/develop/steps/build.md, workflows/develop/README.md, workflows/epic/steps/build.md, docs/concepts.md, docs/reference/commands.md, docs/reference/workflow-contract.md

package test

// pf221_decided_plan_contract_test.go (polyforge#221, AC4): the texts
// that state the plan's decided-rulings duty carry its four elements —
// every item of every ticket section opening `## 已定` mapped to the
// unit that carries it or a stated `none — <reason>`, a ticket without
// such a section asking for no mapping section, build's close and
// review judging it by the one same facts.py plan-conformance line, and
// an unmapped ruling red and named with its number and text. The rule
// reads the ticket's own writing only: no pinned sentence names a
// project, so any repository's ticket written this way is read the
// same. Whitespace-flattened, so a rewrap keeps them pinned.

import (
	"strings"
	"testing"
)

func TestPf221PlanMapsTheDecidedRulings(t *testing.T) {
	for rel, wants := range map[string][]string{
		"workflows/_shared/skills/build.md": {
			// (1) every item of every ## 已定 section mapped, or reasoned
			"in which every item of every such section (numbered across the sections in order of appearance) is mapped to the unit that carries it, or carries `none — <reason>` (docs-only, or a confirmation of the state as it stands)",
			// (2) no such section: no mapping asked
			"a ticket without such a section omits the mapping",
			// (3)+(4) one entry, a red unmapped ruling named
			"The close and review judge it by the same `facts.py plan-conformance` line: an unmapped ruling is red, named with its number and text",
		},
		"workflows/develop/steps/build.md": {
			"in which every item of every such section is mapped to the unit that carries it, or carries `none — <reason>`",
			"a ticket without such a section omits the mapping",
			"The close and review judge it by the same `facts.py plan-conformance` line: an unmapped ruling is red, named with its number and text",
		},
		"workflows/develop/README.md": {
			"its `## 已定对照` section mapping every item of the ticket's `## 已定` sections to a unit or a stated reason (a ticket without one omits it; an unmapped ruling the close's plan-conformance names red, and review judges by the same line)",
		},
		"workflows/epic/steps/build.md": {
			"a plan this lane writes maps the ticket's `## 已定` rulings in its `## 已定对照` section, as the skill states, an unmapped ruling red",
		},
		"docs/concepts.md": {
			"A plan written on a ticket that carries one or more `## 已定`-opening sections also carries a `## 已定对照` section mapping each of their items to the unit that carries it, or to a stated reason",
			"the close's plan-conformance names any unmapped ruling red (its number and text), and review runs the same line",
		},
		"docs/reference/commands.md": {
			"the plan's `## 已定对照` mapping against the work item's decided rulings",
			"an unmapped ruling is red, named with its number and text",
			"a ticket without such a section asks for no mapping section",
		},
		"docs/reference/workflow-contract.md": {
			"requires every item of every section opening `## 已定` — numbered across the sections in order of appearance — mapped in the plan's `## 已定对照` section to a unit or a stated `none` reason",
			"an unmapped ruling is red, named with its number and text, and build's close and review run the one same line",
		},
	} {
		flat := w68Flat(t, rel)
		for _, w := range wants {
			if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
				t.Errorf("%s must state %q", rel, w)
			}
			if strings.Contains(strings.ToLower(w), "polyforge") {
				t.Errorf("%s: the decided-rulings rule reads the ticket's own writing only — its statement %q names a project", rel, w)
			}
		}
	}
}
