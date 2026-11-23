// carries: skills/polyforge/SKILL.md, skills/polyforge/verbs/file.md, skills/polyforge/verbs/submit.md, skills/polyforge/verbs/watch.md

package test

// pf232_followup_promise_skill_contract_test.go (polyforge#232, AC1–AC3): a
// text contract. The routing skill gains one session rule: when a session
// promises the owner to do something once work finishes, it chooses one of
// two and says which — start the host's own background monitor (which wakes
// the session on completion or a decision-feed terminal, and may only run a
// mechanical action the owner already approved for that exact case), or tell
// the owner the result lands in Pi Web's Polyforge panel decision feed and
// promise no session-side follow-up. A one-shot sub-agent must never start a
// monitor; the rule fires only on a promise. The "Human decisions" section
// stops claiming there is no watcher to start, and file.md, submit.md and
// watch.md point at the same rule from the places a promise is made.
//
// Honesty label: a skill is prose and no test executes a session agent. The
// deliverable is the text itself, so the text is the observable behaviour
// here; the pins hold one semantic token per duty (trigger, both
// dispositions, the monitor's limits, host neutrality), never a whole
// sentence, so a rewording that keeps the duty stays green while dropping it
// turns this red. The cross-reference assertions are section-scoped: a
// pointer placed anywhere else in a fragment does not count.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	pf232Skill  = "skills/polyforge/SKILL.md"
	pf232File   = "skills/polyforge/verbs/file.md"
	pf232Submit = "skills/polyforge/verbs/submit.md"
	pf232Watch  = "skills/polyforge/verbs/watch.md"

	// pf232RuleRef is the one reference string every cross-reference carries:
	// the rule's section and its number in that section.
	pf232RuleRef = "SKILL.md's follow-up rule (`Rules for you`, rule 4)"
)

// pf232Section answers the whitespace-flattened body of a section: from its
// heading to the next heading of the same or a higher level. The fragments
// are hard-wrapped, so every pin runs on the flattened text.
func pf232Section(flat, heading, nextPrefix string) string {
	i := strings.Index(flat, heading)
	if i < 0 {
		return ""
	}
	rest := flat[i+len(heading):]
	if j := strings.Index(rest, nextPrefix); j >= 0 {
		return rest[:j]
	}
	return rest
}

// TestPF232FollowupPromiseSkillContract pins the rule itself (AC1) and its
// host neutrality and placement (AC3).
func TestPF232FollowupPromiseSkillContract(t *testing.T) {
	skill := w68Flat(t, pf232Skill)
	rules := pf232Section(skill, "### Rules for you (every harness)", " ## ")
	if rules == "" {
		t.Fatalf("%s lacks the `### Rules for you (every harness)` section — the follow-up promise rule lives there", pf232Skill)
	}

	// 4. the rule, numbered in the section, after the progress-question rule
	if !strings.Contains(rules, "4. **A follow-up promise needs a means.**") {
		t.Errorf("%s: the fourth `Rules for you` rule must be the follow-up promise rule", pf232Skill)
	}

	// AC1: the trigger is a promise the session made, and only a promise.
	for _, want := range []string{
		"promise the owner",
		"only on a promise you made",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("%s: the follow-up rule lacks the trigger %q — it fires only on a promise the session made", pf232Skill, want)
		}
	}

	// AC1 disposition 1: the host's background monitor, what it polls, when it
	// exits, and that exiting wakes the session.
	for _, want := range []string{
		"background-task capability",
		"`pf task show",
		"`pf queue`",
		"`pf watch <wi> --once`",
		"finishes or gets stuck",
		"wakes the session",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("%s: the follow-up rule lacks the background-monitor duty %q", pf232Skill, want)
		}
	}

	// AC1: the monitor's mechanical action is bounded by the owner's prior
	// approval, a mechanically decidable trigger and no new judgment;
	// otherwise it only notifies.
	for _, want := range []string{
		"mechanical action",
		"owner has already approved",
		"mechanically decidable",
		"adds no new judgment",
		"only notifies",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("%s: the follow-up rule lacks the mechanical-action bound %q — otherwise the monitor only notifies", pf232Skill, want)
		}
	}

	// AC1 disposition 2: a one-shot sub-agent never starts a monitor, and the
	// session says where the result appears instead, with no promise.
	for _, want := range []string{
		"one-shot sub-agent",
		"never start a monitor",
		"Pi Web's Polyforge panel decision feed",
		"`pf watch decisions`",
		"no session-side follow-up promise",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("%s: the follow-up rule lacks the no-monitor duty %q", pf232Skill, want)
		}
	}

	// AC3: host-neutral. A host tool may be named only as an example, never
	// as the only way, and at least one other host is named beside it.
	if strings.Contains(rules, "bg_run") {
		for _, want := range []string{"e.g.", "Claude Code", "examples only"} {
			if !strings.Contains(rules, want) {
				t.Errorf("%s: the follow-up rule names bg_run without the host-neutral marker %q — a host tool is an example, never the requirement", pf232Skill, want)
			}
		}
	}

	// AC3 structure: the rule stays out of Session rules, whose count and
	// numbering the older contract tests pin (w202's "Nine standing rules",
	// pf101's "session rule 9").
	if !strings.Contains(skill, "Nine standing rules") {
		t.Errorf("%s: the Session rules lead-in no longer reads \"Nine standing rules\" — the new rule belongs to `Rules for you`, not Session rules", pf232Skill)
	}
	if strings.Contains(skill, "Ten standing rules") {
		t.Errorf("%s: the Session rules count moved to ten — the follow-up rule belongs to `Rules for you`, not Session rules", pf232Skill)
	}
	session := pf232Section(skill, "## Session rules", " ## ")
	if strings.Contains(session, "A follow-up promise needs a means") {
		t.Errorf("%s: the follow-up rule was inserted into Session rules — it belongs to `Rules for you`, which keeps the standing-rule count unchanged", pf232Skill)
	}
}

// TestPF232HumanDecisionsAndCrossReferences pins AC2: the "Human decisions"
// section no longer contradicts the new rule, and the three fragments point
// at that same rule from the place a follow-up promise is made.
func TestPF232HumanDecisionsAndCrossReferences(t *testing.T) {
	skill := w68Flat(t, pf232Skill)

	human := pf232Section(skill, "## Human decisions", " ## Session rules")
	if human == "" {
		t.Fatalf("%s lacks the `## Human decisions` section", pf232Skill)
	}
	// The daemon-side truth survives: the execution plane still wakes nobody.
	if !strings.Contains(human, "The execution plane never wakes a session") {
		t.Errorf("%s `## Human decisions` no longer states \"The execution plane never wakes a session\" — the retired daemon watcher must not come back", pf232Skill)
	}
	// The absolute claim the new rule contradicts is gone.
	if strings.Contains(human, "There is no watcher to start") {
		t.Errorf("%s `## Human decisions` still claims \"There is no watcher to start\" — a session's own promise monitor is exactly that", pf232Skill)
	}
	// The progress-question rule is qualified rather than a blanket no-poll.
	if strings.Contains(skill, "Do not poll.") {
		t.Errorf("%s still carries the unqualified \"Do not poll.\" — a promise's monitor polls in the background", pf232Skill)
	}
	if !strings.Contains(human, "Do not poll in the foreground") {
		t.Errorf("%s `## Human decisions` lacks the qualified \"Do not poll in the foreground\" — the foreground/background distinction is what reconciles the rules", pf232Skill)
	}

	// file.md: the pre-submit analysis section points at the rule.
	fileSection := pf232Section(w68Flat(t, pf232File), "### Pre-submit analysis", " ### ")
	if fileSection == "" {
		t.Fatalf("%s lacks the `### Pre-submit analysis` section", pf232File)
	}
	if !strings.Contains(fileSection, pf232RuleRef) {
		t.Errorf("%s `### Pre-submit analysis` does not carry %q — a session that promised to check the draft must know how it learns the draft is written", pf232File, pf232RuleRef)
	}

	// submit.md: the DEPENDENCY_BLOCKED row points at the rule. The row is
	// read raw, one table line, so a pointer elsewhere in the file cannot
	// satisfy it.
	raw, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(pf232Submit)))
	if err != nil {
		t.Fatalf("read %s: %v", pf232Submit, err)
	}
	row := ""
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.Contains(ln, "`DEPENDENCY_BLOCKED`") && strings.Contains(ln, "CORE-409") {
			row = ln
			break
		}
	}
	if row == "" {
		t.Fatalf("%s has no `CORE-409` `DEPENDENCY_BLOCKED` table row", pf232Submit)
	}
	if !strings.Contains(row, pf232RuleRef) {
		t.Errorf("%s's `DEPENDENCY_BLOCKED` row does not carry %q — \"submit again once it is wrapped\" is a follow-up promise", pf232Submit, pf232RuleRef)
	}

	// watch.md: the verb's intro stops claiming there is no watcher to start
	// and points at the rule a session's monitor follows.
	watch := w68Flat(t, pf232Watch)
	intro := watch
	if i := strings.Index(intro, " ## Triggers"); i >= 0 {
		intro = intro[:i]
	}
	if strings.Contains(intro, "watcher to start") {
		t.Errorf("%s still claims there is a \"watcher to start\" — the verb itself starts none, but a promised session's monitor does", pf232Watch)
	}
	if !strings.Contains(intro, pf232RuleRef) {
		t.Errorf("%s's intro does not carry %q — the reader of this verb is the one who starts the monitor", pf232Watch, pf232RuleRef)
	}
}
