package workflow_test

// v2_keys_test.go: the agent-step keys contract 2 introduced that
// contract 4 keeps — skill, resources (admission, the merge lock),
// budget.tool_minutes, model_tier with defaults.tiers and waivable — each
// accepted in its declared shape and refused with its WFC code otherwise.
// The keys contract 4 removed are v4_keys_test.go's.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// skillFile is a well-formed skill.
const skillFile = "---\nname: tidy\ndescription: a fixture skill\ncommands: [pf-run]\n---\n\n1. Run `pf-run --json`.\n"

func TestV2KeysAccepted(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, workflow.SharedDir, "skills", "tidy.md"), skillFile)
	dir := workflowtest.WriteLaneUnchecked(t, root, "demo", `max_rewinds: 1
defaults:
  model_candidates: [pi:prov-a/model-a1]
  tiers:
    cheap: [pi:prov-c/small]
    mid: [pi:prov-c/small, claude:prov-b/model-b1]
steps:
  - id: draft
    skill: tidy
    budget: { wall_minutes: 30, tool_minutes: 20 }
    model_tier: cheap
    resources: { admission: heavy, locks: [merge] }
  - id: check
    gate: true
    waivable: true
    on_fail: { target: draft }
  - id: last
    model_tier: mid
`)
	workflowtest.FillMissing(t, dir)
	lane, errs := workflow.Load(dir)
	if len(errs) > 0 {
		t.Fatal(workflow.AsLoadError(dir, errs))
	}
	draft, check, last := lane.Step("draft"), lane.Step("check"), lane.Step("last")
	if draft.Skill != "tidy" || draft.ToolMinutes != 20 || draft.ModelTier != "cheap" || !draft.Resources.Heavy() || !draft.Resources.Holds(workflow.LockMerge) {
		t.Errorf("draft = %+v", draft)
	}
	if !check.Waivable || check.OnFailTarget != "draft" {
		t.Errorf("check = %+v", check)
	}
	if strings.Join(lane.TierCandidates(last), ",") != "pi:prov-c/small,claude:prov-b/model-b1" {
		t.Errorf("last = %+v tier %v", last, lane.TierCandidates(last))
	}
	sk, err := workflow.ReadSkill(dir, "tidy")
	if err != nil || sk.Name != "tidy" || strings.Join(sk.Commands, ",") != "pf-run" || !strings.Contains(sk.Body, "pf-run --json") {
		t.Errorf("ReadSkill = %+v %v", sk, err)
	}
}

// TestV2KeysRefused: each refused shape produces exactly its code at the
// key (a lane that is otherwise valid).
func TestV2KeysRefused(t *testing.T) {
	cases := []struct {
		name, front, code, at string
	}{
		{"tool_minutes over the bound", "steps:\n  - id: work\n    budget: { tool_minutes: 181 }\n", workflow.CodeInvalidValue, "steps[0](work).budget.tool_minutes"},
		{"tool_minutes over wall_minutes", "steps:\n  - id: work\n    budget: { wall_minutes: 10, tool_minutes: 20 }\n", workflow.CodeInvalidValue, "steps[0](work).budget.tool_minutes"},
		{"waivable on a non-gate", "steps:\n  - id: work\n    waivable: true\n", workflow.CodeInvalidValue, "steps[0](work).waivable"},
		{"model_tier beside model_candidates", "defaults: { tiers: { cheap: [pi:p/m] } }\nsteps:\n  - id: work\n    model_tier: cheap\n    model_candidates: [pi:p/m]\n", workflow.CodeInvalidValue, "steps[0](work).model_tier"},
		{"model_tier naming no tier", "defaults: { tiers: { cheap: [pi:p/m] } }\nsteps:\n  - id: work\n    model_tier: strong\n", workflow.CodeModelCandidate, "steps[0](work).model_tier"},
		{"model_tier without a table", "steps:\n  - id: work\n    model_tier: cheap\n", workflow.CodeModelCandidate, "steps[0](work).model_tier"},
		{"a tier candidate malformed", "defaults: { tiers: { cheap: [nope] } }\nsteps:\n  - id: work\n", workflow.CodeModelCandidate, "defaults.tiers.cheap[0]"},
		{"resources.admission unknown", "steps:\n  - id: work\n    resources: { admission: light }\n", workflow.CodeInvalidValue, "steps[0](work).resources.admission"},
		{"resources.locks unknown", "steps:\n  - id: work\n    resources: { locks: [door] }\n", workflow.CodeInvalidValue, "steps[0](work).resources.locks[0]"},
		{"resources.locks twice", "steps:\n  - id: work\n    resources: { locks: [merge, merge] }\n", workflow.CodeInvalidValue, "steps[0](work).resources.locks[1]"},
		{"skill name malformed", "steps:\n  - id: work\n    skill: Not/Here\n", workflow.CodeInvalidValue, "steps[0](work).skill"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			dir := workflowtest.WriteLaneUnchecked(t, root, "demo", c.front)
			workflowtest.FillMissing(t, dir)
			_, errs := workflow.Load(dir)
			if len(errs) != 1 || errs[0].Code != c.code || errs[0].Path != c.at {
				t.Fatalf("defects %v, want one %s at %s", errs, c.code, c.at)
			}
		})
	}
}

// TestV2SkillLaneOverridesShared: the lane's own skills/<name>.md wins over
// the shared one, and a skill file with an unknown frontmatter key is
// WFC-200 at that file.
func TestV2SkillLaneOverridesShared(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, workflow.SharedDir, "skills", "tidy.md"), skillFile)
	dir := workflowtest.WriteLaneUnchecked(t, root, "demo", "steps:\n  - id: work\n    skill: tidy\n")
	workflowtest.FillMissing(t, dir)
	own := filepath.Join(dir, workflow.SkillsDir, "tidy.md")
	write(t, own, strings.Replace(skillFile, "a fixture skill", "the lane's own", 1))
	if got := workflow.SkillPath(dir, "tidy"); got != own {
		t.Fatalf("SkillPath = %s, want the lane's own %s", got, own)
	}
	if sk, err := workflow.ReadSkill(dir, "tidy"); err != nil || sk.Description != "the lane's own" {
		t.Fatalf("ReadSkill = %+v %v", sk, err)
	}
	write(t, own, "---\nname: tidy\ndescription: d\ncommands: []\nharness: claude\n---\n\nbody\n")
	_, errs := workflow.Load(dir)
	if len(errs) != 1 || errs[0].Code != workflow.CodeSkill || errs[0].File != own || errs[0].Path != "harness" {
		t.Fatalf("defects %v, want one WFC-200 at the skill's unknown key", errs)
	}
	if _, err := workflow.ReadSkill(dir, "tidy"); err == nil || !strings.Contains(err.Error(), workflow.CodeSkill) {
		t.Fatalf("ReadSkill must refuse the defective file: %v", err)
	}
}

// TestV2WaivableDefaultsToFalse: `waivable` is a gate's opt-in. A gate
// that does not declare it loads not waivable; `waivable: true` loads
// waivable; `waivable: false` stays not waivable; a non-bool value is
// refused.
func TestV2WaivableDefaultsToFalse(t *testing.T) {
	root := t.TempDir()
	dir := workflowtest.WriteLaneUnchecked(t, root, "demo", `max_rewinds: 1
steps:
  - id: draft
  - id: plain
    gate: true
    on_fail: { target: draft }
  - id: opted
    gate: true
    waivable: true
    on_fail: { target: draft }
  - id: declined
    gate: true
    waivable: false
    on_fail: { target: draft }
`)
	workflowtest.FillMissing(t, dir)
	lane, errs := workflow.Load(dir)
	if len(errs) > 0 {
		t.Fatal(workflow.AsLoadError(dir, errs))
	}
	for id, want := range map[string]bool{"plain": false, "opted": true, "declined": false} {
		s := lane.Step(id)
		if s.Waivable != want {
			t.Errorf("gate %s: waivable %t, want %t", id, s.Waivable, want)
		}
	}
	dir = workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", "steps:\n  - id: work\n  - id: check\n    gate: true\n    waivable: yes please\n")
	workflowtest.FillMissing(t, dir)
	if _, errs := workflow.Load(dir); len(errs) != 1 || errs[0].Path != "steps[1](check).waivable" {
		t.Fatalf("a non-bool waivable is refused at the key: %v", errs)
	}
}
