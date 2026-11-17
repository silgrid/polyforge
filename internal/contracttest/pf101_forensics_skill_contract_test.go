// carries: skills/polyforge/SKILL.md, docs/upgrade.md

package test

// pf101_forensics_skill_contract_test.go (polyforge#122, AC2): the
// routing skill carries a ninth standing rule — the forensics
// discipline, six duties — beside the eight rules that predate it, and
// the Session rules lead-in counts nine. The six duties are the ones
// the ticket names (owner 2026-11-17, polyforge#101's 补充范围):
// raw data lands on disk first and the conversation carries counts,
// differences and excerpts only; history is located first and then
// read by range; every tool return is bounded (the plan's Q1 value,
// 8 KiB of visible text) and states its truncation; already-read
// material is referenced instead of reloaded while a redirected
// command's exit code is kept; a report filed where it belongs is
// later referenced by conclusions and pointers only; and a task
// prompt or sub-agent brief passes the same constraints downstream,
// naming its raw directory.
//
// The rule's text is project-neutral (the owner's wording decision:
// 「工作区临时目录的专门子目录」, each surface binding its concrete
// directory in its own section — the task verb its raw batch, a step
// its downloads/): no project or component name, no ticket reference
// inside the rule 9 block. The count pin is W202's (updated in the
// same unit); this test pins the six duties, the neutrality and the
// rule's position after rule 8.
//
// Red on the base tree: the rule 9 block does not exist there. The
// assertions are on pinned tokens — a rewording that keeps them stays
// green; dropping a duty, binding the rule to a project or moving it
// before rule 8 turns red.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pf101Rule9Block answers SKILL.md's session rule 9 block: from its
// "9. **" lead-in to the next top-level "## " heading.
func pf101Rule9Block(t *testing.T, skill string) string {
	t.Helper()
	i := strings.Index(skill, "\n9. **")
	if i < 0 {
		t.Fatalf("skills/polyforge/SKILL.md: no session rule 9 — the forensics rule is missing")
	}
	rest := skill[i+1:]
	j := strings.Index(rest, "\n## ")
	if j < 0 {
		t.Fatalf("skills/polyforge/SKILL.md: rule 9 runs past the Session rules section with no following heading")
	}
	return rest[:j]
}

func TestPF101ForensicsSkillRuleContract(t *testing.T) {
	root := v1Root(t)
	raw, err := os.ReadFile(filepath.Join(root, "skills", "polyforge", "SKILL.md"))
	if err != nil {
		t.Fatalf("skills/polyforge/SKILL.md: %v", err)
	}
	skill := string(raw)
	block := pf101Rule9Block(t, skill)

	// Six duties, one bullet each: the ticket's 取证六条.
	bullets := 0
	for _, l := range strings.Split(block, "\n") {
		if strings.HasPrefix(strings.TrimLeft(l, " \t"), "- **") {
			bullets++
		}
	}
	if bullets != 6 {
		t.Errorf("the rule 9 block carries %d duty bullets, want exactly 6 (one per forensics duty)", bullets)
	}
	for _, tok := range []string{
		// 1. raw data lands on disk first; counts, differences and
		// excerpts only — never the full body
		"dedicated subdirectory of the workspace's temporary directory",
		"counts, differences and short evidence excerpts",
		// 2. locate first, then read by range — never a whole history
		"locate",
		"read only the located range",
		// 3. bounded tool returns, the truncation stated
		"8 KiB",
		"states what was cut and where the complete original lies",
		// 4. no reload of what is already read; the original exit
		// code kept
		"referenced, not fetched again",
		"exit code",
		// 5. a filed report is referenced, not restated
		"conclusions and references only",
		// 6. the same constraints passed downstream, the raw
		// directory named
		"naming the directory its raw material lands in",
	} {
		if !strings.Contains(block, tok) {
			t.Errorf("the rule 9 block lacks the pinned token %q — one of the six forensics duties is missing or reworded past recognition", tok)
		}
	}

	// Project-neutral text: no project or component name and no
	// ticket reference inside the rule (the pf101 concise-rule ban
	// list; the concrete directories are bound per surface, outside
	// this block).
	for _, banned := range []string{"polyforge", "aihub", "pi-web", "webui", "(#"} {
		if b := strings.Contains(strings.ToLower(block), banned); b {
			t.Errorf("the rule 9 block names %q — the forensics rule is project-neutral text (each surface binds its own directory in its own section)", banned)
		}
	}

	// Position: rule 9 follows rule 8 — the clone-sync rule keeps its
	// number, and the four verbs' "SKILL.md session rule 8" pointers
	// stay valid.
	if i8, i9 := strings.Index(skill, "8. **Sync the session clone"), strings.Index(skill, "9. **"); i8 < 0 || i9 < 0 || i8 > i9 {
		t.Errorf("session rule 9 must sit after rule 8 (clone sync keeps its number; the rule-8 pointers in the verbs stay valid)")
	}

	// The upgrade note: an append-only section documents the new rule
	// (skills ship by reference from the release checkout, so an
	// upgrade changes every session's discipline).
	upgrade, err := os.ReadFile(filepath.Join(root, "docs", "upgrade.md"))
	if err != nil {
		t.Fatalf("docs/upgrade.md: %v", err)
	}
	if !strings.Contains(string(upgrade), "## v0.2.4, the forensics standing rule") {
		t.Errorf("docs/upgrade.md lacks the appended forensics-rule section (append-only; the w254 pin moves in the same commit)")
	}
}
