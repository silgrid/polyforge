package driver

// w109_workflow_text_test.go: pins the shipped workflow
// texts to the driver behavior they describe, mechanically.
//
//  1. TestW109ReviewReentryFixSurfaceIsFirstParentNonMerge (G6) — the
//     review's later-round section (contract 3's re-entry fragment, the
//     review skill's "A later round" section since workflow contract 4),
//     as the review dispatch renders it, states the fix surface as the
//     first-parent, non-merge commits since the heads the earlier round
//     judged: a plain <head>..HEAD range also carries the upstream changes
//     sync's machine-made default-branch merge brought in. Red on the
//     pre-fix text ("the diff from the last verdict round's recorded heads
//     to the current HEAD").
//  2. TestW109VerificationFailedFetchIsIncomplete (G7) — the shared
//     verification definition's merge section makes a failed fetch outcome
//     `incomplete`, never a bar run on an unfetched tree. Red on the
//     pre-fix text (the section named no fetch failure).
//  3. TestW109GrillTextsStandOnTheirOwn (P6) — no shipped workflow text
//     leans on "the filing skill" for a definition (a name with no location
//     or content for an adopter that does not carry polyforge's routing
//     skill); the grills state the deepening points and the
//     Entry-confirmation readiness reading inline. Red on the pre-fix grill
//     texts ("the filing skill's `Down-lane check`", "defined once in the
//     filing skill").
//
// The file adds no production code, no driver field and no config key.

import (
	"strings"
	"testing"
)

// w109Flat collapses all whitespace runs to one space, so a phrase
// matches however the markdown wraps it.
func w109Flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestW109ReviewReentryFixSurfaceIsFirstParentNonMerge(t *testing.T) {
	const heading = "## A later round: the two faces and the round contract"
	sec := w79Section(spawnStepPrompt(t, "develop", reviewStepID), heading)
	if sec == "" {
		t.Fatalf(" G6: the review prompt renders no %q section (the review skill's later-round rules)", heading)
	}
	flat := w109Flat(sec)
	if strings.Contains(flat, "the diff from the last verdict round's recorded heads to the current HEAD") {
		t.Error(" G6: the later round still defines the fix surface as the plain heads..HEAD diff — after a passed round that range carries the upstream changes a machine-made default-branch merge brought in")
	}
	if !strings.Contains(flat, "The fix surface is each task repo's first-parent, non-merge commits since the head the latest earlier round judged: `git -C repos/<name> log --first-parent --no-merges -p <head>..HEAD`") {
		t.Error(" G6: the fresh face must name the fix surface's command — the first-parent, non-merge commits since the judged heads")
	}
	if !strings.Contains(flat, "Never read it off `git diff <head>..HEAD`: a sync may have merged the default branch since, and none of what it brought in is the fix.") {
		t.Error(" G6: the fresh face must rule out the plain <head>..HEAD diff, which carries what a sync merged in")
	}
	// the round contract's fix-surface discovery is scoped to that surface
	if !strings.Contains(flat, "From the second round on, a blocking finding is one of: a residual; a new defect on the fix surface;") {
		t.Errorf(" G6: the round contract's fix-surface discovery must be scoped to the fix surface (the first-parent, non-merge commits):\n%s", flat)
	}
}

func TestW109GrillTextsStandOnTheirOwn(t *testing.T) {
	for _, f := range w79ShippedWorkflowTexts(t) {
		if strings.Contains(strings.ToLower(w109Flat(f.text)), "filing skill") {
			t.Errorf(" P6: %s names \"the filing skill\" — workflows/ stays self-contained: state the definition inline instead of pointing at a skill an adopter may not carry", f.path)
		}
	}
}
