package driver

// principles_test.go: workflows/_shared/principles.md is rendered once
// into the grill/spec/plan/code-like steps of every shipped bundle and
// into the shared review step; polyforge-only rules live in the repo's
// AGENTS.md, never in the project-neutral workflows.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// principledSteps is every shipped (bundle, step) that carries the
// principles; every other shipped step carries none.
var principledSteps = map[string][]string{
	"feature-full": {"grill", "spec", "plan", "code", "review"},
	"autopilot":    {"grill", "fix"},
	"bug-fix":      {"root-cause", "fix", "review"},
	"chore":        {"plan", "code", "review"},
}

func TestShippedPrinciplesRenderedOnce(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, workflow.PrinciplesFile))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(raw))
	want := map[string]bool{}
	for bundle, steps := range principledSteps {
		for _, s := range steps {
			want[bundle+"/"+s] = true
		}
	}
	seen := 0
	for _, bs := range shippedBundleSteps(t) {
		key := filepath.Base(bs[0]) + "/" + bs[1]
		def, err := readStepDefinition(bs[0], bs[1], shippedVars(t, bs[0], bs[1]))
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		n := strings.Count(def, text)
		if !want[key] {
			if n != 0 {
				t.Errorf("%s renders the principles %d times, want none", key, n)
			}
			continue
		}
		seen++
		if n != 1 || strings.Count(def, "\n## Principles\n") != 1 {
			t.Errorf("%s: principles rendered %d times / %d `## Principles` sections, want exactly 1 each", key, n, strings.Count(def, "\n## Principles\n"))
		}
		// the step's own usage paragraph precedes the shared text
		usage := def[strings.Index(def, "\n## Principles\n")+len("\n## Principles\n"):]
		i := strings.Index(usage, text)
		if i < 0 || len(strings.TrimSpace(usage[:i])) < 40 {
			t.Errorf("%s: no step-specific usage paragraph before the principles", key)
		}
		// the prompt budget: the whole section stays within 2050 bytes.
		// Raised from 1500 (the old text left 4 bytes of headroom) by the
		// waiting principle (~510 bytes): it must reach
		// every principled step, so the budget grows by that much and no
		// more — it still stops any other growth of the shared text.
		if n := len("\n## Principles\n") + i + len(text); n > 2050 {
			t.Errorf("%s: `## Principles` section is %d bytes, budget 2050", key, n)
		}
	}
	if seen != len(want) {
		t.Errorf("found %d of %d principled steps among the shipped bundles", seen, len(want))
	}
	// end to end: a materialized capsule renders them into the dispatched prompt
	for _, bs := range [][2]string{{"feature-full", "review"}, {"chore", "plan"}} {
		if p := spawnStepPrompt(t, bs[0], bs[1]); strings.Count(p, text) != 1 {
			t.Errorf("%s/%s dispatched prompt carries the principles %d times, want 1", bs[0], bs[1], strings.Count(p, text))
		}
	}
}

// The rules only polyforge itself follows stay out of the shipped
// workflows (they are generic tools) and live in the repo's AGENTS.md,
// which CLAUDE.md imports.
func TestPolyforgeOnlyRulesInAgentsMD(t *testing.T) {
	banned := []string{"backward-compatibility", "backward compatibility", "compat shim", "daemon or aihub", "daemon/aihub"}
	err := filepath.WalkDir(shippedWorkflowsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".md" {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		flat := strings.ToLower(strings.Join(strings.Fields(string(raw)), " "))
		for _, b := range banned {
			if strings.Contains(flat, b) {
				t.Errorf("%s carries the polyforge-only rule %q (it belongs in AGENTS.md)", p, b)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(shippedWorkflowsDir, "..")
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"No backward-compatibility code.", "state the\n  condition under which it is deleted", "A new daemon or aihub entry point states whether a step agent\n  can reach it"} {
		if !strings.Contains(string(agents), w) {
			t.Errorf("AGENTS.md lacks %q", w)
		}
	}
	if claude, err := os.ReadFile(filepath.Join(root, "CLAUDE.md")); err != nil || string(claude) != "@AGENTS.md\n" {
		t.Errorf("CLAUDE.md must be the one-line import of AGENTS.md: %q, %v", claude, err)
	}
}
