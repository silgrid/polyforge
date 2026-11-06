package driver

// w521_publish_last_step_test.go: when the lane's merge
// step is skipped, publish is the lane's last step (the hand-off opening
// notes it so) — its shared skill carries the branch for that case: the PR
// body's line that nobody in the lane merges the PR, the published.py line
// written out in full, the wrap-attrs registration of the record it writes,
// and the wrap summary before done. The lane's own merge stays the default:
// the line "do not merge it by hand" is still there, and the step
// definition says the merge step runs after publish only when it runs.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW521PublishPromptCarriesTheLastStepBranch(t *testing.T) {
	const publishedLine = "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/published.py\" --publish artifacts/publish.json --verified artifacts/verify/result.json --record-out artifacts/publish/integration.json --json --out artifacts/publish/record.json`"
	wants := []string{
		publishedLine,
		"`pf step hub wrap-attrs integration @artifacts/publish/integration.json`",
		"This lane does not merge this PR — it awaits a human review.",
		"do not merge it by hand",
		"`{state: published, repos: [{repo, pr, head, light_verified_head}]}`",
		"notes you as the last step",
		"write the wrap summary (the step header's Memory section), then hand off `done`",
	}
	// develop's publish, and micro's (the same shared skill: its merge is
	// not skippable, so its branch never triggers, but the prompt renders)
	for _, bundle := range []string{developBundle, "micro"} {
		flat := w111Flat(spawnStepPrompt(t, bundle, publishStepID))
		for _, w := range wants {
			if !strings.Contains(flat, w111Flat(w)) {
				t.Errorf("%s/%s: the prompt must carry %q", bundle, publishStepID, w)
			}
		}
	}
	// the skill names its script; the step definition's merge sentence is
	// conditional, never "the merge step right after you merges the PR"
	root := filepath.Join("..", "..", "..", "workflows", "_shared")
	skill, err := os.ReadFile(filepath.Join(root, "skills", "publish.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(skill), "\ncommands: [published.py]\n") {
		t.Errorf("skills/publish.md: commands: must be [published.py]")
	}
	for _, p := range []string{filepath.Join(root, "steps", "publish.md"), filepath.Join(root, "skills", "publish.md")} {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		flat := w111Flat(string(raw))
		if strings.Contains(flat, "merge step right after you merges") || strings.Contains(flat, "merges the PR right after you") {
			t.Errorf("%s: the merge step runs after publish only when it is not skipped; the sentence must say so", p)
		}
	}
}
