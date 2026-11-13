package test

// w78_plan_conformance_and_false_routing_acceptance_test.go: —
// the coupled-obligation rules live in the repo's AGENTS.md, not in the
// project-neutral workflows. The retired lanes' plan-conformance and
// false-routing scenarios are deleted (owner 2026-11-09: deprecated lanes
// retired, their tests are not maintained).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW78CouplingRulesLiveInAgentsMd(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("w78: %s must be readable: %v", rel, err)
		}
		return string(b)
	}
	// The generic couplings only: the sync-list and ledger couplings hold
	// only in the internal tree (the public projection drops them), so
	// their pin lives in an internal-only test.
	agents := read("AGENTS.md")
	if !strings.Contains(agents, "Coupled obligations") {
		t.Error("w78: AGENTS.md must carry the project's coupled-obligations section")
	}
	if !strings.Contains(agents, "skills/polyforge/verbs/doctor.md") {
		t.Errorf("w78: AGENTS.md must name the known coupling %q", "skills/polyforge/verbs/doctor.md")
	}
	// every shipped step definition (the retired lanes' plan steps this
	// once named went with)
	steps, err := filepath.Glob(filepath.Join(root, "workflows", "*", "steps", "*.md"))
	if err != nil || len(steps) == 0 {
		t.Fatalf("w78: no shipped step definitions found (%v)", err)
	}
	for _, p := range steps {
		rel, _ := filepath.Rel(root, p)
		if s := read(rel); strings.Contains(s, "docs/sync/") || strings.Contains(s, "skills/polyforge/") {
			t.Errorf("w78: %s names a polyforge-specific path; workflows/ must stay project-neutral", rel)
		}
	}
}
