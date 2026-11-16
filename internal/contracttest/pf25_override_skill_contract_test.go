package test

// pf25_override_skill_contract_test.go: a text contract. Work item
// polyforge#25 routes the `pf override` CLI face (the step-overlay verb)
// into the routing skill: one intent-table row, a new verbs/override.md
// fragment and the verb count in the SKILL.md description — the same
// shape #59's `task` routing took. The change is skill text plus this
// pin — no endpoint or protocol moves here — so the only mechanical
// contract is the shipped text itself.
//
// Red on the base tree: neither verbs/override.md nor the description's
// advertising exists there. Green only once the routing text shipped.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	pf25SkillRel = filepath.Join("skills", "polyforge", "SKILL.md")
	pf25FragRel  = filepath.Join("skills", "polyforge", "verbs", "override.md")
)

func TestPF25OverrideSkillContract(t *testing.T) {
	root := v1Root(t)

	raw, err := os.ReadFile(filepath.Join(root, pf25SkillRel))
	if err != nil {
		t.Fatalf("%s is the routing skill the override intent must be routable from: %v", pf25SkillRel, err)
	}
	skill := string(raw)

	// 1. The description is the only text a session loads unconditionally:
	// it must name `override` and carry the bumped verb count.
	desc, err := w246FrontmatterDescription(skill)
	if err != nil {
		t.Fatalf("%s's frontmatter must parse as strict YAML (the description carries the override advertising): %v", pf25SkillRel, err)
	}
	for _, tok := range []string{"16 routing verbs", "override"} {
		if !strings.Contains(desc, tok) {
			t.Errorf("the skill description must advertise the override verb — the marker %q is missing; the description is the only text a harness loads unconditionally, so without it a session never discovers `pf override`", tok)
		}
	}

	// 2. The intent table routes the intent to the fragment, and the body's
	// verb count matches the description.
	for _, tok := range []string{"`verbs/override.md`", "跳过这张票的某步", "换个模型再继续"} {
		if !strings.Contains(skill, tok) {
			t.Errorf("%s must route the override intent to `verbs/override.md` — the marker %q is missing", pf25SkillRel, tok)
		}
	}
	if !strings.Contains(skill, "decide which of the 16 routing verbs") {
		t.Errorf("%s's body must say 16 routing verbs — the description and the body sentence must not disagree on the verb count", pf25SkillRel)
	}

	// 3. The fragment exists and carries the shipped surface's facts: the
	// command shapes, the precedence chain, the parked-ticket windows, the
	// waive guidance and the typed refusals.
	fragRaw, err := os.ReadFile(filepath.Join(root, pf25FragRel))
	if err != nil {
		t.Fatalf("%s must exist — the fragment that owns the override routing discipline; the intent table has no row to point at without it: %v", pf25FragRel, err)
	}
	frag := string(fragRaw)
	for _, tok := range []string{
		"pf override <id>",
		"--step <step-id> --models",
		"--clear-models",
		"--skip",
		"--unskip",
		"--by <user>",
		"step-overrides.json",
		"the overlay > `pf submit --models`",
		"waive",
		"skippable",
		"DAEMON-403",
		"DAEMON-409",
		"DAEMON-400",
		"models.json",
		"override_set",
		"pf watch",
	} {
		if !strings.Contains(frag, tok) {
			t.Errorf("%s must carry the override surface marker %q — the fragment exists but does not teach the surface shipped", pf25FragRel, tok)
		}
	}
}
