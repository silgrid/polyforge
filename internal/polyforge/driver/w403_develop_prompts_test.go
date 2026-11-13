package driver

// w403_develop_prompts_test.go (A1; workflow contract v2 PR
// 1 and PR 12): the develop build and review prompts, rendered from a
// materialized capsule, carry no grill — build asks the owner only on a
// surprise, right after the plan, through `pf step ask` (its question
// table), e2e authoring as units and review's e2e quality and owner-reply
// check. Since workflow contract 4 build records each decision it asked
// for in its hand-off's 关键决定和理由, and review reads the owner's words
// in the messages the daemon quotes in build's section headers (contract
// 3's `summary` and `steps.build.answers`).

import (
	"strings"
	"testing"
)

func TestDevelopPromptsAskOnlyOnSurprisesAndCarryE2E(t *testing.T) {
	for step, c := range map[string]struct{ wants, stale []string }{
		"build": {wants: []string{
			"## 3a. Plan", "Do not grill it again: no clarity checklist, no batched round of questions",
			"**Ask only in these cases** — run `pf step ask \"<question>\"`, then end your turn; the answer is your next turn. Never at the end of your work; without one of these you do not ask:",
			"(a) the code reveals something the ticket did not decide", "(b) the plan diverges from the ticket",
			"(c) the ticket says `confirm_plan: true` — right after `artifacts/plan.md` is written and before the first test",
			"(d) no acceptance section, or an incomplete one — once, right after the plan, the list you propose in the question.",
			"The owner replies in free text (\"按你的建议来\", \"ok but drop AC3\", a list): that reply is the decision, whatever its words.",
			"**关键决定和理由**: every decision you asked the owner for — the message's `at`, the owner's words verbatim and how you read them (`review` checks your reading against the words the daemon quotes in your header)",
			"A plan the owner replied to is the judged list — never judge against a list only you wrote.",
			"`## Non-goals`", "| 问题 | 推荐选项 | 理由 | 重点关注程度 |", "an e2e scenario is a unit like any other",
			"never a fixed delay instead of polling; a bounded poll deadline is fine",
		}, stale: []string{"## Clarity", "`build-grill`", "3a. Grill", "reason `grill`", "Copy each answer verbatim into `artifacts/grill-qa.md`",
			"reason `dead_end`", "`question_id` `confirm-plan`", "`build-dead-end`", "`build-deviation`", "`build-acceptance`", "pf bar-run", "## What the driver checks",
			// contract 3's ask command and answers record
			"`pf ask", "build.answers"}},
		reviewStepID: {wants: []string{
			"**e2e quality (`test_weakened`).**", "**Owner replies (`guarantee`).**",
			"an owner reply (the owner messages the daemon quotes in build's section headers) that the plan's `## Acceptance` / `## Non-goals` or the diff ignores or contradicts",
			"A deviation from the ticket, or an acceptance list `build` wrote itself (a ticket without an acceptance section, or an incomplete one), needs an owner reply on record",
			"build's 关键决定和理由 must state the decision and its reading consistently with the owner's raw words",
			"a first unit `test_commit` older than the owner's reply to the plan",
			"a suite the verification-scope engine discovers",
		}, stale: []string{"`build-grill`", "`## Clarity`", "`build-deviation`", "# Build facts", "`pf ask", "steps.build.answers", "`build`'s summary"}},
	} {
		p := spawnStepPrompt(t, developBundle, step)
		flat := strings.Join(strings.Fields(p), " ") // a rewrap keeps them pinned
		for _, w := range c.wants {
			if !strings.Contains(flat, w) {
				t.Errorf("develop/%s prompt must carry %q", step, w)
			}
		}
		for _, s := range c.stale {
			if strings.Contains(flat, s) {
				t.Errorf("develop/%s prompt still carries %q (the grill left develop with workflow contract v2 PR 1, the v1 stops and the daemon records with PR 12, `pf ask` and the answers record with contract 4)", step, s)
			}
		}
		if strings.Contains(p, "{{") {
			t.Errorf("develop/%s prompt carries an unrendered placeholder", step)
		}
	}
}
