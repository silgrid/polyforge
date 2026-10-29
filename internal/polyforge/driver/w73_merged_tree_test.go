package driver

// w73_merged_tree_test.go: pins the SHIPPED TEXT of the
// merged-tree verification proof. Every delivery lane's dispatched
// verification prompt carries the fetch + clean merge before the bar
// (with the abort-on-conflict, no-halfway-state/restart and
// machine-made-merge rules) — the release bundle (which has no
// verification step) carries none of it — the deliver prompt states the
// no-re-run consequence and the earlier-dispatch rule, and the retired
// unmerged-tree wording is gone from the shipped files. The authored
// acceptance suite (test/w73_merged_head_acceptance_test.go) proves the
// behaviour end to end through the fake harness; this file pins the text
// the daemon itself dispatches, on the lanes the shared body reaches.
// It adds no production code and no driver field, event or check.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w73DutyHeader opens the shared verification step's merged-tree duty
// section — the paragraph every delivery lane renders verbatim (the
// shared body is one file, so a lane-specific variant or off-switch
// would have to be quoted text somewhere).
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

// w73DutySection extracts the merged-tree duty section from the shipped
// shared verification step, header included.
func w73DutySection(t *testing.T) string {
	t.Helper()
	raw := w73Read(t, "workflows/"+workflow.SharedDir+"/steps/verification.md")
	start := strings.Index(raw, w73DutyHeader)
	if start < 0 {
		t.Fatalf("shipped verification.md lost its merged-tree duty section header %q", w73DutyHeader)
	}
	rest := raw[start:]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// TestW73MergedTreeDutyRidesEveryDeliveryLane: the rendered verification
// prompt of each delivery lane carries the whole duty section verbatim
// (no lane-specific opt-out) plus every duty phrase: the fetch, the clean
// merge, the bar-after-the-merge-on-the-merged-HEAD duty, the legal
// no-op, the abort-on-conflict duty, the no-halfway-state/restart note
// and the machine-made-merge rule.
func TestW73MergedTreeDutyRidesEveryDeliveryLane(t *testing.T) {
	section := w73DutySection(t)
	// Raw-substring phrases: each must stay one unbroken run of text in
	// the dispatched prompt (the authored acceptance suite matches the
	// same way).
	duty := []string{
		"git fetch origin",
		"git merge --no-edit origin/<default>",
		"Already up to date",
		"AFTER that merge, on the merged",
		"git merge --abort",
		"a resumed dispatch simply repeats the fetch and merge",
		"machine-made",
	}
	for _, lane := range []string{"feature-full", "bug-fix", "chore", "autopilot"} {
		prompt := spawnStepPrompt(t, lane, verificationStepID)
		for _, want := range duty {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s: the rendered verification prompt lost the merged-tree duty phrase %q", lane, want)
			}
		}
		if !strings.Contains(prompt, section) {
			t.Errorf("%s: the rendered verification prompt does not carry the shared duty section verbatim (a lane-specific variant or opt-out?)", lane)
		}
	}
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

// TestW73DeliverPromptStatesMergedTreeConsequence: every delivery lane's
// rendered deliver prompt states that verification proved the merged
// tree (an unmoved default branch records no test_bar command at all)
// and that verification's own bar run — an earlier dispatch — never
// covers a delivery whose merge moved HEAD, while the existing re-run
// rule it qualifies stays in place.
func TestW73DeliverPromptStatesMergedTreeConsequence(t *testing.T) {
	for _, lane := range []string{"feature-full", "bug-fix", "chore", "autopilot"} {
		prompt := spawnStepPrompt(t, lane, deliverStepID)
		for _, want := range []string{
			"Verification proved the merged tree",
			"records **no** `test_bar`",
			"verification's own bar run",
			// the re-run rule the new statement qualifies, kept verbatim.
			"re-run every `test_bar` command of this",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s: the rendered deliver prompt lost the merged-tree consequence phrase %q", lane, want)
			}
		}
	}
}

// TestW73RetiredUnmergedTreeWordingIsGone: the shipped files state the
// merged-tree proof, and the replaced wording is deleted, not kept
// beside the new rule (the TestW19TierLaneTextContentContract pattern:
// file reads, whitespace-normalised so the phrases may wrap).
func TestW73RetiredUnmergedTreeWordingIsGone(t *testing.T) {
	verification := w73Flat(w73Read(t, "workflows/"+workflow.SharedDir+"/steps/verification.md"))
	for _, gone := range []string{
		// the bar's object was the tree the build step produced, unmerged.
		"on the tree the build step produced",
		// the test_bar description's unconditional deliver re-run.
		"re-runs exactly these after merging the latest default branch",
	} {
		if strings.Contains(verification, gone) {
			t.Errorf("shipped verification.md still carries the retired wording %q", gone)
		}
	}
	for _, want := range []string{
		"pass on the merged tree",
		"re-runs exactly these commands only when its own merge moves HEAD past the merged head this run proved (and unconditionally after a light pass)",
	} {
		if !strings.Contains(verification, want) {
			t.Errorf("shipped verification.md lost the merged-tree phrase %q", want)
		}
	}
	// The no-re-run condition is HEAD equality, never the merge reporting
	// "Already up to date": an earlier dispatch may already have merged.
	deliver := w73Flat(w73Read(t, "workflows/"+workflow.SharedDir+"/steps/deliver.md"))
	if !strings.Contains(deliver, "after this step's merge, `git rev-parse HEAD` equals `verified_heads.<name>`") {
		t.Errorf("shipped deliver.md does not key the merged-tree no-re-run on HEAD equality")
	}
	if strings.Contains(deliver, "reports \"Already up to date\"), HEAD equals the verified head") {
		t.Errorf("shipped deliver.md still keys the no-re-run on \"Already up to date\"")
	}
	if strings.Contains(w73Flat(w73Read(t, "docs/user-guide.md")), "then the machine bar verifies the tree it passed") {
		t.Errorf("docs/user-guide.md still carries the retired unmerged-tree wording")
	}
	for rel, want := range map[string][]string{
		"docs/concepts.md": {
			"Verification proves the merged tree",
			"`verified_heads` names that merged head",
			"git merge --abort",
			"records no test-bar run",
		},
		"docs/bundles.md": {
			"the machine bar on the merged tree: what review passed merged with the latest origin default branch",
			"`verified_heads`, the merged head verification proved",
		},
		"docs/user-guide.md": {
			"then the machine bar verifies the tree review passed merged with the latest origin default branch",
		},
		"docs/design/polyforge-vnext/01-workflow-skills.md": {
			"on the tree review already passed merged with the latest origin default branch",
		},
		"workflows/chore/workflow.md": {
			"re-runs the machine bar on the tree that review passed merged with the latest origin default branch",
		},
		"workflows/feature-full/workflow.md": {
			"re-run on delivery when deliver's own merge moves HEAD past the verified head (and unconditionally after a light pass)",
			"the machine gate verifies the tree the judgment already passed merged with the latest origin default branch",
		},
		"workflows/_shared/steps/review.md": {
			"it runs the full bar on the tree you judged merged with the latest origin default branch",
			"`deliver` re-runs that same bar only when its own merge moves HEAD past the verified head",
			"the machine gate behind you verifies the tree you already judged merged with the latest origin default branch",
			// review's own coverage is unchanged: it still judges the
			// unmerged diff (spec non-goal).
			"so judge the diff itself",
		},
	} {
		flat := w73Flat(w73Read(t, rel))
		for _, w := range want {
			if !strings.Contains(flat, w) {
				t.Errorf("%s does not state the merged-tree proof phrase %q", rel, w)
			}
		}
	}
	// The sweep is complete: none of the shipped files still states the
	// unmerged-tree shape the merged-tree proof replaced (sentence-final
	// punctuation, so the restatements that append "merged with …" do not
	// trip the guard).
	for _, gone := range []struct{ rel, phrase string }{
		{"workflows/feature-full/workflow.md", "the machine gate verifies the tree the judgment already passed."},
		{"workflows/_shared/steps/review.md", "bar on the tree you judged, and"},
		{"workflows/_shared/steps/review.md", "only after merging a moved default branch changed the tree"},
		{"workflows/_shared/steps/review.md", "the tree you already judged."},
	} {
		if strings.Contains(w73Flat(w73Read(t, gone.rel)), gone.phrase) {
			t.Errorf("%s still carries the retired unmerged-tree wording %q", gone.rel, gone.phrase)
		}
	}
}
