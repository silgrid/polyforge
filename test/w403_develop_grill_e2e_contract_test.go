// carries: workflows/develop/*, workflows/develop/fragments/*, workflows/_shared/steps/*, docs/concepts.md, docs/contributing.md, skills/polyforge/*

package test

// w403_develop_grill_e2e_contract_test.go (A1, rewritten by
// workflow contract v2 PR 1 and PR 12 — the stops are build's asks and
// no daemon check judges the build; workflow contract 4: the ask is
// `pf step ask`, the owner's replies are quoted in build's section headers
// and build's reading of them is its hand-off's 关键决定和理由): the texts
// that state when the develop lane's
// build asks the owner — only on a surprise, right after the plan, never
// at completion; no grill, the requirement having been grilled in the
// session that filed the ticket — e2e authoring inside build (an e2e
// scenario is a unit, its suite's `carries:` covers what the build
// touched) and review's e2e quality and owner-reply checks agree with
// each other and with what review now judges. Whitespace-flattened,
// so a rewrap keeps them pinned.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// developStale are the texts the develop lane's files (its workflow.yaml,
// its review's judge fragment and its README.md) no longer carry.
var developStale = []string{"**Clarify**", "**Grill**", "a fixed delay or timeout", "`## Clarity`", "`build-grill`", "stops: [grill, acceptance]",
	// workflow contract v2 PR 12
	"checks: [", "sweep: {", "evidence: { mode: notarized }", "facts: build-facts", "`build-dead-end`", "the driver also refuses a completion"}

func TestW403DevelopGrillAndE2EContract(t *testing.T) {
	root := v1Root(t)
	for rel, c := range map[string]struct{ wants, stale []string }{
		"workflows/develop/steps/build.md": {wants: []string{
			"Do not grill it again: no clarity checklist, no batched round of questions; what the ticket, the code and recalled memory settle, you decide and record.",
			"**When to ask the owner**: only in the four cases your skill names — (a) a dead end or a contradiction, (b) a deviation, (c) `confirm_plan: true`, (d) a missing or incomplete acceptance list — right after the plan, never at the end of your work, with `pf step ask`.",
			"for a user-visible criterion its e2e scenario or one line why a unit test suffices",
			"an e2e scenario is a unit like any other",
			"never a fixed delay instead of polling; a bounded poll deadline is fine — and measures only once the state has settled",
			"every suite file opens with a `carries:` header (`// carries: <glob>, ...`, `#` where comments start with `#`); in a suite you add, or whose run a unit records, its globs, relative to the suite's project root",
		}, stale: []string{"build-clarify", "enum: [clarify,", "enum: [grill,", "never a fixed delay or timeout", "## Clarity", "`build-grill`", "3a. Grill", "approval",
			// workflow contract v2 PR 12: the stops are pf asks, the runs the build's own
			"`needs_input`", "`question_id`", "pf bar-run", "## Bindings", "## What the driver checks", "acceptance_missing"}},
		"workflows/_shared/skills/build.md": {wants: []string{
			"(a) the code reveals something the ticket did not decide — a dead end, a ticket that contradicts the code or itself",
			"(b) the plan diverges from the ticket — what diverges and why;",
			"(c) the ticket says `confirm_plan: true` — right after `artifacts/plan.md` is written and before the first test, with the acceptance list and the units;",
			"(d) no acceptance section, or an incomplete one — once, right after the plan, the list you propose in the question.",
			"The owner replies in free text (\"按你的建议来\", \"ok but drop AC3\", a list): that reply is the decision, whatever its words.",
			"**关键决定和理由**: every decision you asked the owner for — the message's `at`, the owner's words verbatim and how you read them (`review` checks your reading against the words the daemon quotes in your header)",
			"A plan the owner replied to is the judged list — never judge against a list only you wrote.",
		}, stale: []string{"`build-grill`", "`## Clarity`", "`needs_input`", "pf bar-run"}},
		"workflows/develop/workflow.yaml": {wants: []string{
			"skill: build", "skill: review", "resources: { admission: heavy }",
		}, stale: developStale},
		"workflows/develop/fragments/review-judge.md": {wants: []string{
			"4. **e2e quality (`test_weakened`).**",
			"a scenario synchronised by a fixed delay instead of polling the condition it awaits (a bounded poll deadline is fine)",
			"on a suite the verification-scope engine discovers (an npm, python or rust root's default discovery or its `suite_glob=`; never a Go `test/` file)",
			"one that asserts or measures before the awaited state has settled",
			"an owner reply (the owner messages the daemon quotes in build's section headers) that the plan's `## Acceptance` / `## Non-goals` or the diff ignores or contradicts",
			"**Owner replies (`guarantee`).** A deviation from the ticket, or an acceptance list `build` wrote itself (a ticket without an acceptance section, or an incomplete one), needs an owner reply on record",
			"build's 关键决定和理由 must state the decision and its reading consistently with the owner's raw words. A deviation or self-written list with no reply, a reading the words do not support (an \"ok but drop AC3\" read as a plain yes), or — on a ticket carrying `confirm_plan: true` — a first unit `test_commit` older than the owner's reply to the plan, is a blocking `guarantee` finding",
			"So is a user-visible criterion whose plan names neither an e2e scenario nor why a unit test suffices",
		}, stale: developStale},
		"workflows/develop/README.md": {wants: []string{
			"It asks the owner (`pf step ask`) only when the code reveals something the ticket did not decide, when the plan diverges from the ticket, when the ticket carries `confirm_plan: true`, or when it has no acceptance list or an incomplete one — right after the plan, never at the end.",
			"1. **A surprise in build** — a dead end or a contradiction, a deviation, `confirm_plan: true`, a missing or incomplete acceptance list: once, right after the plan.",
			"No daemon check enforces either stop",
		}, stale: developStale},
		"workflows/_shared/steps/review.md": {wants: []string{
			"an owner's reply to the build's questions (its `pf step ask`, quoted verbatim in the build's section headers) that the acceptance criteria or the change ignore",
			"an e2e scenario waiting by a fixed delay instead of polling (a bounded poll deadline is fine) or asserting before the awaited state settles; a suite the verification-scope engine discovers (by default or `suite_glob=`) whose `carries:` header is missing or does not name the paths it exercises",
			"a deviation from the ticket, or an acceptance list the build wrote itself, with no owner reply on record, or a reading of the owner's reply its words do not support (develop: the build's 关键决定和理由 against the owner's quoted replies) — a decision the build took for the owner",
		}, stale: []string{"a fixed delay or timeout", "`build-grill`", "`## Clarity`", "text-only sweep"}},
		"workflows/_shared/build_subagents.md": {wants: []string{
			"one sub-agent reproduces and locates a reported defect and writes `artifacts/plan.md` — not you.",
			"It does not grill the ticket: no clarity checklist, no batched round of questions — the ticket was settled before it was filed.",
			"You ask the owner — a sub-agent cannot stop the step — only in the cases 3a names, right after the plan; folding the answer into the plan is the sub-agent's, when you dispatch it again with the owner's words.",
		}, stale: []string{"grills the work item", "`## Clarity`", "`artifacts/grill-qa.md`", "recorded #<n>", "`needs_input`"}},
		"docs/concepts.md": {wants: []string{
			"`build` does not grill the ticket (the requirement was grilled in the session that filed it, and `pf submit` refuses an execution ticket without an acceptance section); it asks the owner (`pf step ask`) only on a surprise, right after the plan and never at completion",
			"the ticket carries `confirm_plan: true` (one stop right after the plan, before the first test), or the ticket has no acceptance list or an incomplete one (never self-written: `build` proposes it and asks once with the plan",
			"The owner answers in free text, which `build` reads: its hand-off states each decision, the owner's words and its reading, and review checks the reading against the words",
			"No daemon check judges what `build` reports (workflow contract v2)",
			"an e2e scenario timed by a fixed delay, measuring before its state settles or carrying a wrong `carries:` is `test_weakened`",
		}, stale: []string{"`build` asks once for a batched clarification when the ticket is ambiguous", "`build` grills the ticket first", "`build-grill`", "`## Clarity`", "bare approval word", "`build-acceptance`", "one `confirm-plan` stop"}},
		"docs/contributing.md": {wants: []string{
			"`build` does not grill the ticket; it asks the owner only on a surprise, right after the plan and never at completion, with `pf step ask`.",
			"Human stops: a dead end or a contradiction the code reveals (a defect `build` cannot reproduce included); the plan diverging from the ticket; `confirm_plan` (one stop right after the plan, before the first test, when the ticket says `confirm_plan: true`)",
			"No daemon check settles `build` (workflow contract v2)",
			"every e2e suite the build adds or changes declaring a well-formed `carries:` — matching a path it touched under the suite's project root when the build added it or a unit's run names it — (the engine's suites: an npm, python or rust root's `suite_glob=`, else its default discovery)",
			"review's `facts.py carries` lists a suite it adds or whose run a unit records whose `carries:` is missing, malformed, or matches none of the paths the build touched under the suite's project root that are not test, documentation or forced-full paths, and a suite it only edits whose `carries:` is missing or malformed",
			"an e2e scenario timed by a fixed delay, measuring before its state settles or carrying a wrong `carries:` is `test_weakened`",
		}, stale: []string{"Human stops: a batched clarification at `build`", "Human stops: the grill", "`build-grill`", "`## Clarity`", "`build-dead-end`", "`build-deviation`"}},
		"docs/user-guide.md": {wants: []string{
			"It does not grill the ticket (that happened in the session that filed it); it stops for you only on a surprise, right after the plan",
		}, stale: []string{"`build-grill`", "--question build-dead-end"}},
		"skills/polyforge/SKILL.md": {wants: []string{
			"as are a develop build's stops (its `pf step ask` after the plan: the owner answers in free text that the build reads)",
		}, stale: []string{"`build-grill`", "`build-acceptance`"}},
		"skills/polyforge/verbs/execute-intent.md": {wants: []string{
			"and so are a develop build's stops (its `pf step ask` after the plan: the owner answers in free text that the build reads)",
		}, stale: []string{"`build-grill`", "`build-acceptance`"}},
		"skills/polyforge/verbs/file.md": {wants: []string{
			"`build` does not grill the ticket (that is this session's job, before `pf submit`): it asks the owner (its own `pf step ask`) only on a surprise, right after the plan and never at completion",
			"The stops are human-only — never answer them for the owner, who answers in free text the build reads",
			"`develop`'s other human stops: a defect it cannot reproduce (a dead end)",
		}, stale: []string{"other human stops: a batched clarification", "`build-grill`", "`## Clarity`", "`build-acceptance`"}},
	} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		flat := strings.Join(strings.Fields(string(raw)), " ")
		for _, w := range c.wants {
			if !strings.Contains(flat, w) {
				t.Errorf("%s must state %q", rel, w)
			}
		}
		for _, s := range c.stale {
			if strings.Contains(flat, s) {
				t.Errorf("%s still states %q (develop stopped grilling with workflow contract v2 PR 1)", rel, s)
			}
		}
	}
}
