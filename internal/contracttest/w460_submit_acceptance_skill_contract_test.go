// carries: skills/polyforge/verbs/submit.md, skills/polyforge/SKILL.md, skills/polyforge/verbs/file.md

package test

// w460_submit_acceptance_skill_contract_test.go: a text contract.
//
// The routing skill's submit action checks the ticket for acceptance before
// `pf submit` runs: no criterion under `## Acceptance` or `## 验收` means stop,
// tell the user the ticket needs interaction first, grill in the session (what,
// why, scope, what it does not do, when it is done; a read-only subagent may
// survey the code), write the decisions into the background and testable
// criteria marked `— carried by build` into `## Acceptance`, then submit.
// Experiments are done in the session and only their conclusion goes into the
// ticket; the lane rule keeps `micro` to pure text inside `micro_path`.
// Filing itself does not grill. `pf submit`'s own refusal (AC2) is the
// mechanical backstop; this test pins the prompt text, the only mechanical
// contract a session agent's skill has (the w205/w202 convention).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW460SubmitAcceptanceSkillContract(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s is routing-skill text the acceptance check must extend: %v", rel, err)
		}
		return string(b)
	}

	// verbs/submit.md owns the procedure.
	submitRel := filepath.Join("skills", "polyforge", "verbs", "submit.md")
	submit := read(submitRel)
	const heading = "## Before submitting: the acceptance check"
	hi := strings.Index(submit, heading)
	si := strings.Index(submit, "\n## Success")
	switch {
	case hi < 0:
		t.Errorf("%s must carry the section %q — the acceptance check runs before `pf submit`", submitRel, heading)
	case si < 0 || hi > si:
		t.Errorf("%s's %q must come before `## Success` (the check runs before `pf submit`)", submitRel, heading)
	}
	section := ""
	if hi >= 0 {
		section = submit[hi+len(heading):]
		if end := strings.Index(section, "\n## "); end >= 0 {
			section = section[:end]
		}
	}
	for _, want := range []string{
		"`## Acceptance` or `## 验收`",
		"这张票需要先交互、补齐验收才能提交",
		"what", "why", "scope", "what it does not do", "when it is done",
		"read-only subagent",
		"— carried by build",
		"--allow-no-acceptance",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's acceptance-check section must carry %q — no criterion means stop, tell the user 「这张票需要先交互、补齐验收才能提交」, grill in the session (what, why, scope, what it does not do, when it is done; a read-only subagent may survey the code), write testable criteria marked `— carried by build` into `## Acceptance`, then submit; `--allow-no-acceptance` only on the owner's explicit say-so", submitRel, want)
		}
	}
	paras := strings.Split(section, "\n\n")
	paraWith := func(marker string) (string, bool) {
		for _, p := range paras {
			if strings.Contains(p, marker) {
				return p, true
			}
		}
		return "", false
	}
	if p, ok := paraWith("experiment"); !ok || !strings.Contains(p, "conclusion") {
		t.Errorf("%s's acceptance-check section must say experiments and feasibility checks are done in the session first, with only their conclusion written into the ticket (\"experiment\" and \"conclusion\" in one paragraph)", submitRel)
	}
	if p, ok := paraWith("micro_path"); !ok || !strings.Contains(p, "code or tests") || !strings.Contains(p, "`develop`") {
		t.Errorf("%s's acceptance-check section must state the lane rule in one paragraph: `micro` only for a pure text change inside the repository's `micro_path`; a change to code or tests goes to `develop`", submitRel)
	}
	// No-compat: the old sentence calling a ticket without the section legal
	// to submit is rewritten, not kept.
	for _, p := range strings.Split(submit, "\n\n") {
		if strings.Contains(p, "Acceptance") && strings.Contains(p, "is legal") {
			t.Errorf("%s still says a ticket without an acceptance section is legal to submit; `pf submit` now refuses it:\n%s", submitRel, p)
		}
	}

	// SKILL.md points from inside rule 7 (no new heading).
	skillRel := filepath.Join("skills", "polyforge", "SKILL.md")
	skill := read(skillRel)
	r7 := strings.Index(skill, "7. **Execution work routes into the lifecycle")
	r8 := strings.Index(skill, "8. **Sync the session clone")
	if r7 < 0 || r8 < 0 || r8 < r7 {
		t.Fatalf("%s lost session rule 7 or 8", skillRel)
	}
	rule7 := skill[r7:r8]
	for _, want := range []string{"acceptance check", "verbs/submit.md", "Filing does not grill"} {
		if !strings.Contains(rule7, want) {
			t.Errorf("%s session rule 7 must carry %q — submit runs the acceptance check of verbs/submit.md first; filing does not grill", skillRel, want)
		}
	}

	// verbs/file.md: creation may record a bare idea.
	fileRel := filepath.Join("skills", "polyforge", "verbs", "file.md")
	if !strings.Contains(read(fileRel), "Filing does not grill") {
		t.Errorf("%s must say %q — an idea may be filed as-is; the grill happens before submit", fileRel, "Filing does not grill")
	}
}
