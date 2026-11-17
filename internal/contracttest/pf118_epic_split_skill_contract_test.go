package test

// carries: skills/polyforge/SKILL.md, skills/polyforge/verbs/file.md, internal/contracttest/pf118_epic_split_skill_contract_test.go

// pf118_epic_split_skill_contract_test.go (polyforge#118, AC1): the
// routing skill's file verb carries the owner-confirmed epic-split
// filing procedure. Every pin below is scoped to the split section —
// review round 1 (#2) showed the whole-file pins were vacuous
// (DEPENDENCY_BLOCKED, --after and PARENT_BUNDLE_FROZEN live elsewhere
// in file.md at the base) — and the step order is pinned positionally
// (#3): the first unit's child files BEFORE the original moves to the
// epic lane (it holds the original out of the ready queue), and the
// move happens BEFORE the remaining units file (every child then
// points --parent at a ticket already bound to the epic lane, and a
// claim reads the epic lane's children_branch, per Q7's owner-ruled
// order: hold, move, then the rest, then verify and release).

import (
	"regexp"
	"strings"
	"testing"
)

// pf118SplitSection answers the split section of file.md (flattened).
func pf118SplitSection(t *testing.T) string {
	t.Helper()
	return strings.Join(strings.Fields(pf118Section(t, w68Raw(t, "skills/polyforge/verbs/file.md"),
		"### Splitting a ticket into an epic", "## ")), " ")
}

// TestPF118EpicSplitSkillContract pins the routing skill's epic-split
// filing procedure (AC1): every load-bearing step, scoped to the
// section; SKILL.md routes to it without redefining any step; both
// texts stay project-neutral.
func TestPF118EpicSplitSkillContract(t *testing.T) {
	sec := pf118SplitSection(t)
	for _, want := range []string{
		// the owner-confirmed window: the original is queued and never
		// claimed, and the window closes only with the first child
		"never claimed",
		// check-first per unit: the queue attrs query precedes every
		// create and is the duplicate guard on a re-run
		"pf queue --project P --attr split.origin=",
		"--attr split.origin=",
		"--attr split.unit=",
		// old children without the split attrs are checked too
		"pf file show <original>",
		// the first child holds the original out of the ready queue
		"DEPENDENCY_BLOCKED",
		// the original moves to the epic lane
		"pf file edit <original> --bundle epic",
		// sub-tickets point --parent at the epic-bound ticket
		"--parent <original>",
		// sibling order is a dependency, written at create
		"--after <",
		// the move's guard, stated where it belongs (not an ambiguity
		// stop): a child already claimed freezes the lane
		"PARENT_BUNDLE_FROZEN",
		// children stay held until the release: an analysis awaiting
		// the owner's confirmation keeps the auto-claim away
		"auto-claim skips",
		// ambiguity stops the session, never a guessed match
		"looks truncated",
		"a second child for one unit",
		"never guess a match",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("the split section of skills/polyforge/verbs/file.md must carry %q (polyforge#118 AC1, review r1#2/#3)", want)
		}
	}
	// the check precedes the create shape inside the section
	if qi, ci := strings.Index(sec, "pf queue --project P --attr split.origin="), strings.Index(sec, "--parent <original>"); qi < 0 || ci < 0 || qi > ci {
		t.Error("the split procedure's check (pf queue --attr split.origin=) must be stated before the create shape (--parent <original>): check-first is what keeps a re-run from filing a duplicate (polyforge#118 AC1)")
	}
	// the owner-ruled order, positionally: first unit's child → the
	// move to the epic lane → the remaining units (review r1#3)
	iFirst := strings.Index(sec, "The first unit, check first")
	iMove := strings.Index(sec, "pf file edit <original> --bundle epic")
	iRest := strings.Index(sec, "The remaining units, check first")
	if iFirst < 0 || iMove < 0 || iRest < 0 || !(iFirst < iMove && iMove < iRest) {
		t.Errorf("the split procedure must order: first unit's child (holds the original) → pf file edit --bundle epic → the remaining units — got first=%d move=%d rest=%d (polyforge#118 AC1 / Q7, review r1#3)", iFirst, iMove, iRest)
	}

	// SKILL.md routes the split intent to file.md's procedure and
	// defines none of it: one fact, one place.
	skill := w68Flat(t, "skills/polyforge/SKILL.md")
	if !strings.Contains(skill, "verbs/file.md") || !strings.Contains(skill, "upgrade it into an epic") {
		t.Error("skills/polyforge/SKILL.md must route the epic-split intent (\"upgrade it into an epic\") to verbs/file.md's procedure (polyforge#118 AC1)")
	}
	for _, leaked := range []string{"--attr split.origin=", "--bundle epic", "--parent <original>"} {
		if strings.Contains(skill, leaked) {
			t.Errorf("skills/polyforge/SKILL.md must not define the split procedure (%q appears there) — file.md owns it whole; SKILL.md only routes (polyforge#118 AC1)", leaked)
		}
	}

	// the split texts stay project-neutral (AC3): scoped to the NEW
	// texts — the section and SKILL.md's new intent row — not the whole
	// files (the skill's own name and its install paths are not a
	// filing-target hardcode; pf49 owns the whole-face rule).
	skillRaw := w68Raw(t, "skills/polyforge/SKILL.md")
	row := ""
	for _, l := range strings.Split(skillRaw, "\n") {
		if strings.Contains(l, "upgrade it into an epic") {
			row = l
		}
	}
	if row == "" {
		t.Fatal("skills/polyforge/SKILL.md carries no epic-split intent row")
	}
	for rel, text := range map[string]string{
		"skills/polyforge/verbs/file.md (the split section)": sec,
		"skills/polyforge/SKILL.md (the intent row)":          strings.Join(strings.Fields(row), " "),
	} {
		for _, m := range regexp.MustCompile(`polyforge(#[0-9]+)?`).FindAllStringSubmatch(text, -1) {
			if m[1] == "" {
				t.Errorf("%s names the project outside a ticket citation: %q (polyforge#118 AC3: 文字与项目无关)", rel, m[0])
			}
		}
	}
}
