package test

// carries: skills/polyforge/SKILL.md, skills/polyforge/verbs/file.md, internal/contracttest/pf118_epic_split_skill_contract_test.go

// pf118_epic_split_skill_contract_test.go (polyforge#118, AC1): the
// routing skill's file verb carries the owner-confirmed epic-split
// filing procedure — every load-bearing step a session needs to split
// a queued ticket into an epic and its sub-tickets without ever filing
// a duplicate on a re-run. The procedure is defined in file.md alone
// (one fact, one place): SKILL.md only routes the intent to it.

import (
	"regexp"
	"strings"
	"testing"
)

// pf118EpicSplitWants pins file.md's split procedure: each entry is one
// load-bearing fact of AC1 — the check-before-create (no duplicate on a
// re-run), the split attrs as the stable unit key, --parent at the
// epic-bound ticket, the sibling order via --after, the original moved
// to the epic lane, and the stops that keep the window safe.
var pf118EpicSplitWants = []string{
	// the owner-confirmed window: the original is queued and never claimed
	"never claimed",
	// the first child holds the original before anything else moves: a
	// bound parent waits for its children, out of the ready queue
	"DEPENDENCY_BLOCKED",
	// the original moves to the epic lane
	"--bundle epic",
	// sub-tickets point --parent at the epic-bound ticket
	"--parent <original>",
	// check-first, across every status: the queue attrs query precedes
	// the create and is the duplicate guard on a re-run
	"--attr split.origin=",
	"--attr split.unit=",
	"pf queue --project P --attr split.origin=",
	// old children without the split attrs are checked too
	"pf file show <original>",
	// sibling order is a dependency, written at create
	"--after <",
	// ambiguity stops the session: a truncated or ambiguous check is
	// never answered by filing again
	"PARENT_BUNDLE_FROZEN",
}

// TestPF118EpicSplitSkillContract pins the routing skill's epic-split
// filing procedure (AC1): file.md owns it whole, SKILL.md routes to it
// without redefining any step, and the texts stay project-neutral.
func TestPF118EpicSplitSkillContract(t *testing.T) {
	file := w68Flat(t, "skills/polyforge/verbs/file.md")
	for _, want := range pf118EpicSplitWants {
		if !strings.Contains(file, want) {
			t.Errorf("skills/polyforge/verbs/file.md must carry the owner-confirmed split procedure — the load-bearing token %q is missing (polyforge#118 AC1: 先查再建 / --parent / --bundle epic / 兄弟依赖 / 持有窗口)", want)
		}
	}
	// the procedure itself is named: a session reading the verb finds
	// the section by its subject.
	if !strings.Contains(file, "Splitting a ticket into an epic") {
		t.Error("skills/polyforge/verbs/file.md must name its epic-split section (\"Splitting a ticket into an epic\") — a session reading the verb cannot find the procedure without its heading")
	}
	// the check-first order is stated: the queue query runs BEFORE the
	// create of the same unit, so a re-run reuses, never re-files.
	qi := strings.Index(file, "pf queue --project P --attr split.origin=")
	ci := strings.Index(file, "--parent <original>")
	if qi < 0 || ci < 0 || qi > ci {
		t.Error("the split procedure's check (pf queue --attr split.origin=) must be stated before the create shape (--parent <original>): check-first is what keeps a re-run from filing a duplicate (polyforge#118 AC1)")
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

	// the split texts stay project-neutral (AC3): no concrete project is
	// named outside a ticket citation.
	for rel, text := range map[string]string{
		"skills/polyforge/verbs/file.md": file,
		"skills/polyforge/SKILL.md":     skill,
	} {
		for _, m := range regexp.MustCompile(`polyforge(#[0-9]+)?`).FindAllStringSubmatch(text, -1) {
			if m[1] == "" {
				t.Errorf("%s names the project outside a ticket citation: %q (polyforge#118 AC3: 文字与项目无关)", rel, m[0])
			}
		}
	}
}
