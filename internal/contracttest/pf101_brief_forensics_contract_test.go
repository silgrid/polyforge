// carries: workflows/_shared/build_subagents.md, skills/polyforge/SKILL.md

package test

// pf101_brief_forensics_contract_test.go (polyforge#122, AC1): the
// develop build's sub-agent rules require the PARENT step to write the
// forensics output constraints into every sub-agent brief it
// dispatches — the ticket's AC1 wording: build_subagents.md 要求父
// step 把规则写进子 agent 的 brief. A sub-agent cannot be relied on
// to load the rules itself (a safe-mode sub-agent reads no context
// files), so the brief carries them: the sub-agent saves its raw
// material complete into the step's downloads/ directory (the raw-data
// subdirectory a step's capsule agrees on — the ticket's AC3 binds
// it: step 是 downloads/), returns only a summary and an evidence
// index the parent can read on from, keeps each tool return within
// rule 9's bound, and never reloads material it already saved.
//
// The w425 driver test pins the fragment's other halves (rendered
// once, the write-set threshold written once, the pi dispatch fact);
// this test adds only the brief-constraint tokens, and its additions
// introduce no "more than N" / "N or fewer" shape. Red on the base
// tree: the brief bullet carries no output constraints there.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPF101SubagentBriefForensicsContract(t *testing.T) {
	root := v1Root(t)
	raw, err := os.ReadFile(filepath.Join(root, "workflows", "_shared", "build_subagents.md"))
	if err != nil {
		t.Fatalf("workflows/_shared/build_subagents.md: %v", err)
	}
	frag := string(raw)
	flat := strings.Join(strings.Fields(frag), " ")

	// The brief bullet stays the sub-agent's whole brief; the output
	// constraints ride in it.
	i := strings.Index(flat, "**A task is the sub-agent's whole brief.**")
	if i < 0 {
		t.Fatalf("workflows/_shared/build_subagents.md: the whole-brief bullet is gone — this change extends it, it replaces nothing")
	}
	for _, tok := range []string{
		"session rule 9",
		"output constraints",
		"downloads/",
		"a summary and an evidence index",
		"rule 9's bound",
		"reads saved material from the file instead of loading it again",
	} {
		if !strings.Contains(flat, tok) {
			t.Errorf("the brief rules lack the pinned token %q — the parent-step brief-constraint requirement is missing or reworded past recognition", tok)
		}
	}

	// The pinned halves the w425 driver test keeps (spot-checked here
	// so this change is seen to add, not rewrite; whitespace-collapsed
	// like the pins above).
	for _, keep := range []string{
		"Its whole surface is the brief",
		"You write `artifacts/build/units.json` from what the tasks return",
	} {
		if !strings.Contains(flat, keep) {
			t.Errorf("workflows/_shared/build_subagents.md: the w425-pinned token %q is gone — the brief extension rewrites no existing half", keep)
		}
	}
}
