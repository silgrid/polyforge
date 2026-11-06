package workflow_test

// c4_skill_sections_test.go: workflow contract 4 moves what a step reads
// and checks before it starts, and what its hand-off carries, out of lane
// keys (inputs, verifies, outputs) into prose (design #483 §12 row 10,
//). Every step of the four built-in lanes says both: its
// skill carries the two sections, and a step without a skill carries them
// in its own definition. The loader does not judge prose; this test does.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// The two section headings every built-in skill (or skill-less step
// definition) carries, each at the start of a line at any ATX level, an
// English gloss allowed after it.
var c4Sections = map[string]*regexp.Regexp{
	"开工前读什么、核对什么": regexp.MustCompile(`(?m)^#{1,6} 开工前读什么、核对什么(\s|$)`),
	"交接要写什么":      regexp.MustCompile(`(?m)^#{1,6} 交接要写什么(\s|$)`),
}

// TestShippedStepsSayWhatTheyReadAndHandOff: per step of develop, micro,
// integrate and release, its skill file — or, without one, its
// definition — carries both sections.
func TestShippedStepsSayWhatTheyReadAndHandOff(t *testing.T) {
	checked := 0
	for _, name := range []string{"develop", "micro", "integrate", "release"} {
		dir := filepath.Join(shippedLanes, name)
		lane, errs := workflow.Load(dir)
		if len(errs) > 0 {
			t.Fatalf("shipped lane %s refused: %v", name, workflow.AsLoadError(dir, errs))
		}
		for _, s := range lane.Steps {
			var where, text string
			if s.Skill != "" {
				where = workflow.SkillPath(dir, s.Skill)
				raw, err := os.ReadFile(where)
				if err != nil {
					t.Fatalf("%s/%s: skill %s: %v", name, s.ID, s.Skill, err)
				}
				text = string(raw)
			} else {
				def, err := workflow.ReadDefinition(dir, s.ID, s.Vars)
				if err != nil {
					t.Fatalf("%s/%s: definition: %v", name, s.ID, err)
				}
				where, text = def.Path, def.Body
			}
			for heading, re := range c4Sections {
				if !re.MatchString(text) {
					t.Errorf("%s/%s: %s carries no section %q", name, s.ID, where, heading)
				}
			}
			checked++
		}
	}
	if checked < 19 {
		t.Fatalf("checked %d steps, want the four lanes' 19 at least", checked)
	}
}
