package driver

// w109_workflow_text_test.go: pins the shipped workflow
// texts to the driver behavior they describe, mechanically.
//
//  1. TestW109ArmedLaneStatesMechanicalRewindUncharged (G5) — a lane that
//     arms the mechanical fast lane states that the mechanical rewind
//     charges no edge slot (transition.go: charged := !(light || mech ||
//     converging)), never "one slot". Red on the pre-fix feature-full
//     text ("rewind cheaply — one slot", "the light or converging
//     rewinds charge none").
//  2. TestW109ReviewReentryFixSurfaceIsFirstParentNonMerge (G6) — the
//     rendered review re-entry section states the fix surface as the
//     first-parent, non-merge commits since the recorded heads: a plain
//     <heads>..HEAD range also carries the upstream changes verification's
//     (or deliver's) machine-made default-branch merge brought in. Red on
//     the pre-fix text ("the diff from the last verdict round's recorded
//     heads to the current HEAD").
//  3. TestW109VerificationFailedFetchIsIncomplete (G7) — the shared
//     verification definition's merge section makes a failed fetch outcome
//     `incomplete`, never a bar run on an unfetched tree. Red on the
//     pre-fix text (the section named no fetch failure).
//  4. TestW109GrillTextsStandOnTheirOwn (P6) — no shipped workflow text
//     leans on "the filing skill" for a definition (a name with no location
//     or content for an adopter that does not carry polyforge's routing
//     skill); the grills state the deepening points and the
//     Entry-confirmation readiness reading inline. Red on the pre-fix grill
//     texts ("the filing skill's `Down-lane check`", "defined once in the
//     filing skill").
//  5. TestW109NeedsCodeChangeEscalationChargesTheEdgeOnce (T1) — the build
//     steps' needs_code_change field descriptions state what the driver
//     does with the self-report: the fresh re-entry charges the light
//     rewind's edge once (never twice), not "no additional rewind slot".
//  6. TestW109EscalationNoteNamesTheAbandonedRecheck (T2) — the shared
//     fast-lane material's escalation note lists the structural gate FAIL
//     that abandons a pending light recheck among the escalation reasons.
//
// The file adds no production code, no driver field and no config key.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w109Flat collapses all whitespace runs to one space, so a phrase
// matches however the markdown wraps it.
func w109Flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestW109ArmedLaneStatesMechanicalRewindUncharged(t *testing.T) {
	armed := 0
	for _, dir := range shippedBundleDirs(t) {
		wf, err := LoadWorkflow(dir)
		if err != nil {
			t.Fatal(err)
		}
		v := wf.step(verificationStepID)
		if v == nil || !v.MechanicalFix {
			continue
		}
		armed++
		raw, err := os.ReadFile(filepath.Join(dir, "workflow.md"))
		if err != nil {
			t.Fatal(err)
		}
		flat := w109Flat(string(raw))
		if strings.Contains(flat, "rewind cheaply — one slot") {
			t.Errorf(" G5: %s says the mechanical rewind costs \"one slot\" — the driver charges it no edge slot (transition.go charged := !(light || mech || converging)); it counts toward max_total only", dir)
		}
		// Every sentence that lists the uncharged rewinds must name the
		// mechanical one on an armed lane.
		found := false
		for _, sentence := range strings.Split(flat, ". ") {
			if !strings.Contains(sentence, "charge none") {
				continue
			}
			found = true
			if !strings.Contains(sentence, "mechanical") {
				t.Errorf(" G5: %s lists the uncharged rewinds without the mechanical one — an armed lane's mechanical rewind charges no edge slot:\n%s", dir, sentence)
			}
		}
		if !found {
			t.Errorf(" G5: %s states no uncharged-rewind list (\"... charge none\") — the per-edge allowance text must name which rewinds charge no edge slot", dir)
		}
	}
	if armed == 0 {
		t.Fatal(" G5: no shipped lane arms mechanical_fix — the pin has nothing to check")
	}
}

func TestW109ReviewReentryFixSurfaceIsFirstParentNonMerge(t *testing.T) {
	bundle := filepath.Join(shippedWorkflowsDir, "feature-full")
	wf, err := LoadWorkflow(bundle)
	if err != nil {
		t.Fatal(err)
	}
	sec, err := sharedSectionVars(bundle, "review_reentry", wf.stepVars(reviewStepID))
	if err != nil || sec == "" {
		t.Fatalf(" G6: the review re-entry section did not render: %v", err)
	}
	flat := w109Flat(sec)
	if strings.Contains(flat, "the diff from the last verdict round's recorded heads to the current HEAD") {
		t.Error(" G6: the re-entry still defines the fix surface as the plain heads..HEAD diff — after a passed round that range carries the upstream changes a machine-made default-branch merge brought in")
	}
	if !strings.Contains(flat, "git log --first-parent --no-merges -p <head>..HEAD") {
		t.Error(" G6: the fresh face must name the fix surface's command — the first-parent, non-merge commits since the recorded heads")
	}
	contract := w109Flat(w79Section(sec, "## The round contract"))
	if !strings.Contains(contract, "new discovery on the fix surface** — the first-parent, non-merge commits") {
		t.Errorf(" G6: the round contract's fix-surface discovery must be scoped to the first-parent, non-merge commits:\n%s", contract)
	}
}

func TestW109VerificationFailedFetchIsIncomplete(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "verification.md"))
	if err != nil {
		t.Fatal(err)
	}
	merge := w109Flat(w79Section(string(raw), "## Merge the latest default branch before the bar"))
	if merge == "" {
		t.Fatal(" G7: the shared verification definition has no merge section")
	}
	for _, want := range []string{
		"**A failed fetch:**",
		"stop with outcome `incomplete` and the exact error",
		"Never run the bar on the unfetched tree",
	} {
		if !strings.Contains(merge, want) {
			t.Errorf(" G7: the verification merge section must carry %q — a failed `git fetch origin` is outcome incomplete, never a bar on an unfetched tree claimed as the merged proof", want)
		}
	}
}

func TestW109GrillTextsStandOnTheirOwn(t *testing.T) {
	for _, f := range w79ShippedWorkflowTexts(t) {
		if strings.Contains(strings.ToLower(w109Flat(f.text)), "filing skill") {
			t.Errorf(" P6: %s names \"the filing skill\" — workflows/ stays self-contained: state the definition inline instead of pointing at a skill an adopter may not carry", f.path)
		}
	}
	for _, c := range []struct {
		lane string
		want []string
	}{
		{"feature-full", []string{"expert gap", "raw material", "methods and tools", "`Entry confirmation` line", "`scoped-gates readiness` row", "`.polyforge/verify-scope.conf`"}},
		{"autopilot", []string{"expert gap", "raw material", "methods and tools"}},
	} {
		raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, c.lane, "steps", "grill.md"))
		if err != nil {
			t.Fatal(err)
		}
		flat := w109Flat(string(raw))
		for _, want := range c.want {
			if !strings.Contains(flat, want) {
				t.Errorf(" P6: the %s grill must state %q inline — the definition it applies lives in its own text", c.lane, want)
			}
		}
	}
}

func TestW109NeedsCodeChangeEscalationChargesTheEdgeOnce(t *testing.T) {
	for _, rel := range []string{
		filepath.Join("bug-fix", "steps", "fix.md"),
		filepath.Join("chore", "steps", "code.md"),
		filepath.Join("feature-full", "steps", "code.md"),
	} {
		raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		front, _, ok := workflow.SplitFrontmatter(raw)
		if !ok {
			t.Fatalf("%s: no frontmatter", rel)
		}
		flat := w109Flat(string(front))
		if strings.Contains(flat, "charging no additional rewind slot") {
			t.Errorf(" T1: %s still says the needs_code_change escalation charges no slot — the driver charges the light rewind's edge once", rel)
		}
		if !strings.Contains(flat, "charges the edge's rewind slot once (the light rewind itself was uncharged), never twice") {
			t.Errorf(" T1: %s must state the needs_code_change escalation charges the edge once, never twice", rel)
		}
	}
}

func TestW109EscalationNoteNamesTheAbandonedRecheck(t *testing.T) {
	note := w109Flat(mechanicalFixSection(filepath.Join(shippedWorkflowsDir, "feature-full"), "Escalated full re-run (the mechanical fast lane)"))
	if note == "" {
		t.Fatal(" T2: the escalation note did not render")
	}
	if !strings.Contains(note, "a structural gate FAIL (neither text-only nor mechanical") ||
		!strings.Contains(note, "abandoned the pending light recheck") {
		t.Errorf(" T2: the escalation note must list the structural gate FAIL that abandons the pending light recheck:\n%s", note)
	}
	// S1: every fresh re-derivation of the build step abandons
	// the recheck, not only a structural gate FAIL
	for _, want := range []string{"needs_code_change", "root_cause_addressed: false"} {
		if !strings.Contains(note, want) {
			t.Errorf(" S1: the escalation note must name the %s path that abandons the pending light recheck:\n%s", want, note)
		}
	}
}
