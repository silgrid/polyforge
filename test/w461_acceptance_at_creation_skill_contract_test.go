// carries: skills/polyforge/verbs/file.md, skills/polyforge/verbs/followup.md

package test

// w461_acceptance_at_creation_skill_contract_test.go: text contract for
// (AC2).
//
// Filing a ticket whose requirement is very clear — what to do, its scope
// and when it is done all settled, nothing the owner must decide, and when
// unsure it is not clear — writes `## Acceptance` at creation (each item
// verifiable, marked `— carried by build`), so the ticket can be submitted
// directly; otherwise it is filed without acceptance and the submit-time
// check (verbs/submit.md) brings the interaction. A follow-up claim whose
// completion standard can be settled from the wrap's evidence is that case.
// Filing still does not grill (TestW460 pins that line). Sections are
// located by heading, never by position; this test pins the prompt text,
// the only mechanical contract a session agent's skill has.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW461AcceptanceAtCreationSkillContract(t *testing.T) {
	fileRel := filepath.Join("skills", "polyforge", "verbs", "file.md")
	raw, err := os.ReadFile(filepath.Join(v1Root(t), fileRel))
	if err != nil {
		t.Fatalf("read %s: %v", fileRel, err)
	}
	file := string(raw)
	const heading = "\n### Acceptance at creation\n"
	section := ""
	if _, after, ok := strings.Cut(file, heading); ok {
		section = after
		for _, next := range []string{"\n## ", "\n### "} {
			if i := strings.Index(section, next); i >= 0 {
				section = section[:i]
			}
		}
		section = strings.Join(strings.Fields(section), " ")
	} else {
		t.Errorf("%s must carry the subsection %q — when filing writes `## Acceptance`", fileRel, strings.TrimSpace(heading))
	}
	for _, want := range []string{
		"very clear",
		"what to do", "scope", "when it is done",
		"nothing the owner must decide",
		"when unsure, it is not clear",
		"`## Acceptance`",
		"— carried by build",
		"submitted directly",
		"verbs/submit.md",
		"follow-up",
		"wrap",
	} {
		if section != "" && !strings.Contains(section, want) {
			t.Errorf("%s's \"Acceptance at creation\" must carry %q — only a very clear requirement (what to do, scope, when it is done all settled; nothing the owner must decide; when unsure, it is not clear) gets `## Acceptance` at creation, each item verifiable and marked `— carried by build`, so it can be submitted directly; otherwise it is filed without one and the submit-time check (verbs/submit.md) brings the interaction; a follow-up whose completion standard the wrap's evidence settles is this case", fileRel, want)
		}
	}
	if !strings.Contains(file, "Filing does not grill") {
		t.Errorf("%s must keep %q — acceptance at creation is for a requirement already very clear, never a grill", fileRel, "Filing does not grill")
	}

	claim := w152Section(w152Read(t, w152Fragment), "Claim")
	if claim == "" {
		t.Fatalf("%s lost its `## Claim` section", w152Fragment)
	}
	for _, want := range []string{"Acceptance at creation", "wrap", "evidence", "`## Acceptance`"} {
		if !strings.Contains(claim, want) {
			t.Errorf("%s's Claim must carry %q — a claimed item whose completion standard the wrap's evidence settles is filed with `## Acceptance` at creation (verbs/file.md, \"Acceptance at creation\")", w152Fragment, want)
		}
	}
}
