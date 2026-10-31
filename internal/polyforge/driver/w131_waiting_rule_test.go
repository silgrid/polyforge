package driver

// w131_waiting_rule_test.go: the waiting rule reaches the
// long-running steps that never rendered {{principles}} — bug-fix repro,
// feature-full e2e-author, the shared deliver and release verify — so
// their agents await inside one foreground call instead of polling with
// bare sleeps. Red on the pre-change tree (none of the four included
// {{principles}}). The file adds no production code, no driver field and
// no config key.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w131LongRunSteps are the four steps, as (bundle, step); the shared
// deliver is rendered through one delivery lane.
var w131LongRunSteps = [][2]string{
	{"bug-fix", "repro"},
	{"feature-full", "e2e-author"},
	{"feature-full", "deliver"},
	{"release", "verify"},
}

func TestW131WaitingRuleReachesLongRunSteps(t *testing.T) {
	const section = "\n## Principles\n"
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, workflow.PrinciplesFile))
	if err != nil {
		t.Fatal(err)
	}
	shared := strings.TrimSpace(string(raw))
	for _, bs := range w131LongRunSteps {
		bundle, step := bs[0], bs[1]
		key := bundle + "/" + step
		dir := filepath.Join(shippedWorkflowsDir, bundle)
		path := workflow.StepDefinitionPath(dir, step)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if !strings.Contains(string(raw), "{{"+workflow.PrinciplesVar+"}}") {
			t.Errorf("%s does not include {{%s}}", key, workflow.PrinciplesVar)
		}
		rel, _ := filepath.Rel(shippedWorkflowsDir, path)
		if stray := w111StraySleeps(filepath.ToSlash(rel), string(raw)); len(stray) > 0 {
			t.Errorf("%s repeats sleep wording outside the shared rule: %q", key, stray)
		}
		def, err := readStepDefinition(dir, step, shippedVars(t, dir, step))
		if err != nil {
			t.Fatalf("render %s: %v", key, err)
		}
		if n := strings.Count(w111Flat(def), w111Principle); n != 1 {
			t.Errorf("%s renders the waiting principle %d times, want exactly 1", key, n)
		}
		i := strings.Index(def, section)
		if i < 0 {
			t.Errorf("%s has no `## Principles` section", key)
			continue
		}
		usage := def[i+len(section):]
		j := strings.Index(usage, shared)
		if j < 0 {
			t.Errorf("%s: the shared principles are not inside its `## Principles` section", key)
			continue
		}
		head := usage[:j]
		if len(strings.TrimSpace(head)) < 40 {
			t.Errorf("%s has no step-specific usage paragraph before the shared principles", key)
		}
	}
}
