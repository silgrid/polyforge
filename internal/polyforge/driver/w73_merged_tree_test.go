package driver

// w73_merged_tree_test.go: pins the SHIPPED TEXT of the
// merged-tree proof that remains: the release bundle (which has no
// verification step) carries none of the merged-tree duty, and the
// deliver prompt states the no-re-run consequence and the
// earlier-dispatch rule. (Its end-to-end suite drove only retired lanes
// and was deleted, owner 2026-11-09; the agent verification step text
// that carried the duty was deleted — develop's
// verification is the daemon's verify action.)
// It adds no production code and no driver field, event or check.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w73DutyHeader opened the deleted agent verification step's merged-tree
// duty section; no shipped prompt may carry it.
const w73DutyHeader = "## Merge the latest default branch before the bar (the bar proves the merged tree)"

// w73Read answers a shipped file's content, resolved from the repo root
// (the W19 file-read pattern).
func w73Read(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

// w73Flat collapses every whitespace run to one space, so a phrase may
// be asserted across the file's line wrapping.
func w73Flat(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestW73ReleaseLaneCarriesNoMergedTreeDuty: the release bundle declares
// no verification step, and none of its rendered step prompts carries the
// merged-tree duty — the lane is untouched by the change.
func TestW73ReleaseLaneCarriesNoMergedTreeDuty(t *testing.T) {
	wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, "release"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range wf.Steps {
		if s.ID == verificationStepID {
			t.Fatalf("the release bundle must not declare a %s step: %v", verificationStepID, laneOrder(t, "release"))
		}
	}
	for _, stepID := range []string{"release", "verify", "deploy"} {
		prompt := spawnStepPrompt(t, "release", stepID)
		for _, banned := range []string{
			"git merge --no-edit origin/<default>",
			"git merge --abort",
			"Already up to date",
			"verified_heads",
			w73DutyHeader,
		} {
			if strings.Contains(prompt, banned) {
				t.Errorf("release/%s: a lane with no verification step carries the merged-tree duty phrase %q", stepID, banned)
			}
		}
	}
}
