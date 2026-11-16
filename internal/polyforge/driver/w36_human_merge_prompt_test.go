package driver

// w36_human_merge_prompt_test.go (u8): the shared publish and merge
// skills carry the human-merge mode's branches (polyforge#36: a project
// declares `steps.<project>.merge.by: human`, the claim fixes it as the
// capsule's runtime-state/step-modes.json, and only a lane declaring the
// step human_mergeable accepts it) beside the default mode's unchanged
// branches — publish's PR body awaits the person and asks for the
// up-to-date-branches setting instead of forbidding the hand merge;
// merge asks the owner first (the PR links and the verified heads), asks
// again on the script's exit 3 (a closed PR, a mismatched landing, one
// still open) instead of failing back to sync, and carries a revision
// request back to build as an owner rewind. develop's prompts render the
// branches; micro's render the same shared skills and can never take the
// mode (its lane declares no human_mergeable step).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW36SkillsCarryTheHumanMergeBranches(t *testing.T) {
	const humanBody = "此 PR 等待人工审查并由人合并，合并后请回复车道"
	root := filepath.Join("..", "..", "..", "workflows", "_shared")
	publishSkill := w36Read(t, filepath.Join(root, "skills", "publish.md"))
	mergeSkill := w36Read(t, filepath.Join(root, "skills", "merge.md"))
	publishStep := w36Read(t, filepath.Join(root, "steps", "publish.md"))
	mergeStep := w36Read(t, filepath.Join(root, "steps", "merge.md"))

	// publish: the human-mode PR body, the up-to-date-branches hint, and
	// the default mode's line beside it (both branches, one skill)
	for _, want := range []string{
		"runtime-state/step-modes.json",
		humanBody,
		"合并前分支必须最新",
		"do not merge it by hand",
	} {
		if !strings.Contains(publishSkill, want) {
			t.Errorf("skills/publish.md must carry %q (the human-merge branch beside the default one)", want)
		}
	}
	// the mode's condition names the MERGE step's entry of the frozen
	// modes — publish's own step is never in the file (r1#1: a
	// "your step" condition can never be true, and the human PR body
	// would never be written)
	if strings.Contains(publishSkill, "name your step `human`") {
		t.Errorf("skills/publish.md must not condition the human body on its own step's entry (the frozen modes carry the merge step's, never publish's)")
	}
	if !strings.Contains(w111Flat(publishSkill), w111Flat("name the `merge` step `human`")) {
		t.Errorf("skills/publish.md must condition the human body on the frozen modes naming the `merge` step `human`")
	}
	if !strings.Contains(publishStep, "by: human") && !strings.Contains(publishStep, "step-modes.json") {
		t.Errorf("steps/publish.md must name the mode its skill branches on")
	}

	// merge: the ask first (the PR links and the verified heads), the
	// exit-3 ask (never a FAIL back to sync), the revision request's
	// owner rewind to build, and the landed route unchanged
	for _, want := range []string{
		"runtime-state/step-modes.json",
		"pf step ask",
		"PR 链接", "验证过的 head",
		"--status rewind --to build --per-owner",
		"exit 3", "问 owner", "不记 `landed`",
		"wrap-attrs integration @artifacts/merge/landing.json",
	} {
		if !strings.Contains(mergeSkill, want) {
			t.Errorf("skills/merge.md must carry %q (the human-merge branch)", want)
		}
	}
	// the human mode never runs the lane's own merge line
	if strings.Contains(mergeSkill, "human") && !strings.Contains(mergeSkill, "不执行 `gh pr merge`") && !strings.Contains(mergeSkill, "never asks the forge to merge") {
		t.Errorf("skills/merge.md must say the human mode never asks the forge to merge")
	}
	if !strings.Contains(mergeStep, "human") && !strings.Contains(mergeStep, "step-modes.json") {
		t.Errorf("steps/merge.md must name the mode its skill branches on")
	}

	// the prompts render both branches for develop, and micro's prompts
	// render the same shared skills (its lane declares no human_mergeable
	// step, so its branch never triggers)
	for _, bundle := range []string{developBundle, "micro"} {
		flat := w111Flat(spawnStepPrompt(t, bundle, publishStepID))
		if !strings.Contains(flat, w111Flat(humanBody)) || !strings.Contains(flat, "do not merge it by hand") {
			t.Errorf("%s/%s: the prompt carries both PR-body branches", bundle, publishStepID)
		}
		flat = w111Flat(spawnStepPrompt(t, bundle, "merge"))
		if !strings.Contains(flat, "step-modes.json") || !strings.Contains(flat, "--status rewind --to build --per-owner") {
			t.Errorf("%s/merge: the prompt carries the human-merge branches", bundle)
		}
	}
}

func w36Read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
