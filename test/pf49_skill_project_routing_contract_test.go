package test

// carries: skills/polyforge/SKILL.md, skills/polyforge/verbs/file.md, AGENTS.md, test/pf49_skill_project_routing_contract_test.go

// pf49_skill_project_routing_contract_test.go: the routing skill and the
// filing verb hardcode no specific project's routing (polyforge#49, AC7).
// Which project a filing goes to is stated by the workspace's or the
// repository's AGENTS.md — the skill routes by intent only. The
// repository's own team rule lives on, in AGENTS.md's Scope: the rule
// moved out of the skill, not away (owner ruling 2026-11-15: the rule
// itself is unchanged).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pf49SkillRel are the two skill surfaces the ticket names.
var pf49SkillRel = []string{
	"skills/polyforge/SKILL.md",
	"skills/polyforge/verbs/file.md",
}

// pf49Flat reads one repo-relative file, whitespace-flattened (a re-wrapped
// sentence keeps matching).
func pf49Flat(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("polyforge#49: read %s: %v", rel, err)
	}
	return strings.Join(strings.Fields(string(raw)), " ")
}

func TestPF49SkillProjectRoutingContract(t *testing.T) {
	// no hardcoded filing target: neither surface names a concrete
	// project as where work files to — the pre-#49 text sent team work
	// and external contributions to "the **public project**
	// (`polyforge`), the development mainline" and named an internal
	// project. A skill is project-independent; a concrete routing rule
	// belongs to the workspace's or the repository's AGENTS.md.
	for _, rel := range pf49SkillRel {
		flat := pf49Flat(t, rel)
		for _, old := range []string{
			"public project",
			"internal project",
			"development mainline",
		} {
			if strings.Contains(flat, old) {
				t.Errorf("%s still hardcodes a specific project's routing (%q) — which project a filing goes to is the workspace's or the repository's AGENTS.md's to state, never the skill's (polyforge#49 AC7)", rel, old)
			}
		}
		// and each states where the routing rule lives: the workspace's
		// or the repository's AGENTS.md decides the target project.
		if !strings.Contains(flat, "AGENTS.md") {
			t.Errorf("%s must state that which project a filing goes to is defined by the workspace's or the repository's AGENTS.md (polyforge#49 AC7)", rel)
		}
	}
	// the repository's AGENTS.md keeps the team's own routing rule in its
	// Scope — the rule moved out of the skill, not away.
	agents := pf49Flat(t, "AGENTS.md")
	for _, want := range []string{
		"This public repository is the development mainline",
		"external contributions are all filed and developed here",
	} {
		if !strings.Contains(agents, want) {
			t.Errorf("AGENTS.md's Scope keeps the team's routing rule — it must carry %q (polyforge#49 AC7: the rule itself unchanged)", want)
		}
	}
}
