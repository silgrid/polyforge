package test

// owner_review_gate_contract_test.go: text contract for — a
// work item that declares an owner-review artifact has it shown to the owner
// in a deliver needs_input question before any PR merge, and merges only
// after the owner answers; a work item that declares none is asked nothing.
//
// The declaration duty lives once, in the shared fragment
// workflows/_shared/owner_review.md, included by the two plan steps and the
// four build steps; the gate itself is a section of the shared deliver step,
// which keeps its deliver-merge question untouched. Red on the pre-change
// tree: the fragment, the gate section and the includes do not exist there.

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// the six (bundle, step) pairs that may declare an owner-review artifact
var ownerReviewAuthoringSteps = [][2]string{
	{"chore", "plan"}, {"feature-full", "plan"},
	{"chore", "code"}, {"feature-full", "code"},
	{"bug-fix", "fix"}, {"autopilot", "fix"},
}

// the fragment's operative lines (compared whitespace-flattened); each
// renders exactly once per including step
var ownerReviewDutyLines = []string{
	"## Owner review before merge",
	"`deliver` step's owner-review gate",
	"owner-review: <capsule-relative path>[, <path>...] — <one-line description>",
	"That line is the only switch",
	"(`deliver-owner-review`) before `gh pr merge`",
	"「过目通过」",
	"shown read-only and never edited for the review",
	"never declare one for material no human needs to judge",
}

var ownerReviewPlaceholder = regexp.MustCompile(`\{\{[^}]*\}\}`)

// ownerReviewFlat collapses every whitespace run to one space, so a
// re-wrapped paragraph keeps matching
func ownerReviewFlat(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestOwnerReviewGateDeliverContract(t *testing.T) {
	text := w149CarrierRead(t, "workflows/_shared/steps/deliver.md")
	flat := ownerReviewFlat(text)
	gate := strings.Index(text, "## 3. The owner-review gate")
	if gate < 0 {
		t.Fatalf("deliver.md has no `## 3. The owner-review gate` section")
	}
	end := strings.Index(text[gate:], "\n## 4.")
	if end < 0 {
		t.Fatalf("deliver.md's owner-review gate section is not followed by section 4")
	}
	section := ownerReviewFlat(text[gate : gate+end])
	for _, want := range []string{
		"`owner-review:`",
		"`deliver-owner-review`",
		"过目通过",
		"before any `gh pr merge`",
		"skip this section entirely",
		"Silence is a wait, never an approval",
		"read-only",
		"`artifacts/plan.md`",
		"`execution.json`",
		"exactly two",
		// 过目通过 licenses no merge: the rest of the merge rule still binds,
		// and the driver's any-answer check no longer backs it up
		"过目通过 is not a merge decision",
		"ask `deliver-merge` per section 4, even though a human has already answered in this round",
		"The driver's human-answer check counts any answer in the round",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("deliver.md's owner-review gate section lacks %q", want)
		}
	}
	// the merge rule names the gate as one of its conditions
	if !strings.Contains(flat, "the owner-review gate (section 3) is settled") {
		t.Errorf("deliver.md's merge rule does not require the owner-review gate to be settled")
	}
	// the input names where a declaration is read
	inputs := strings.Index(text, "## 1. Inputs")
	perRepo := strings.Index(text, "## 2. Per declared repo")
	if inputs < 0 || perRepo < inputs || !strings.Contains(text[inputs:perRepo], "owner-review:") {
		t.Errorf("deliver.md's inputs do not name the owner-review declaration")
	}
	// un-loosened: the existing human-only question keeps its shape
	for _, keep := range []string{"## 4. The human-only question", "`deliver-merge`", "exactly three answers", "## 5. Result"} {
		if !strings.Contains(text, keep) {
			t.Errorf("deliver.md lost %q", keep)
		}
	}
	if strings.Contains(flat, "human question (section 3)") {
		t.Errorf("deliver.md's pre-flight still points the human question at section 3")
	}
}

func TestOwnerReviewDeclarationReachesAuthoringSteps(t *testing.T) {
	frag := strings.TrimSpace(w149CarrierRead(t, "workflows/_shared/owner_review.md"))
	for _, line := range ownerReviewDutyLines {
		if !strings.Contains(ownerReviewFlat(frag), line) {
			t.Errorf("the owner_review fragment lacks its operative line %q", line)
		}
	}
	for _, bs := range ownerReviewAuthoringSteps {
		bundle, step := bs[0], bs[1]
		rel := "workflows/" + bundle + "/steps/" + step + ".md"
		raw := w149CarrierRead(t, rel)
		if n := strings.Count(raw, "{{owner_review}}"); n != 1 {
			t.Errorf("%s includes {{owner_review}} %d times, want exactly 1", rel, n)
		}
		if strings.Contains(raw, frag) {
			t.Errorf("%s carries a second copy of the owner_review fragment", rel)
		}
		out, err := workflow.ReadStepDefinition(filepath.Join(v1Root(t), "workflows", bundle), step, nil)
		if err != nil {
			t.Fatalf("render %s: %v", rel, err)
		}
		def := string(out)
		if m := ownerReviewPlaceholder.FindString(def); m != "" {
			t.Errorf("%s renders with an unresolved placeholder %s", rel, m)
		}
		flat := ownerReviewFlat(def)
		for _, line := range ownerReviewDutyLines {
			if n := strings.Count(flat, line); n != 1 {
				t.Errorf("%s renders the owner-review line %q %d times, want exactly 1", rel, line, n)
			}
		}
	}
}

func TestOwnerReviewFragmentIsSingleSourceAndLaneNeutral(t *testing.T) {
	frag := w149CarrierRead(t, "workflows/_shared/owner_review.md")
	if m := ownerReviewPlaceholder.FindString(frag); m != "" {
		t.Errorf("the owner_review fragment carries a placeholder %s; a fragment is rendered verbatim", m)
	}
	lower := strings.ToLower(frag)
	for _, name := range []string{"chore", "feature-full", "bug-fix", "autopilot", "polyforge", "ieops"} {
		if strings.Contains(lower, name) {
			t.Errorf("the owner_review fragment names %q; a fragment is lane- and project-neutral", name)
		}
	}
	allowed := map[string]bool{}
	for _, bs := range ownerReviewAuthoringSteps {
		allowed["workflows/"+bs[0]+"/steps/"+bs[1]+".md"] = true
	}
	for _, rel := range w80StepFiles(t) {
		if strings.Contains(w149CarrierRead(t, rel), "{{owner_review}}") && !allowed[rel] {
			t.Errorf("%s includes {{owner_review}} but is not one of the six authoring steps", rel)
		}
	}
}

func TestOwnerReviewCarrierMapsTheGate(t *testing.T) {
	text := ownerReviewFlat(w149CarrierRead(t, "workflows/_shared/deliverable_carrier.md"))
	for _, want := range []string{"owner-review", "`deliver`", "`deliver-owner-review`"} {
		if !strings.Contains(text, want) {
			t.Errorf("the deliverable_carrier fragment does not map an owner-review presentation to the deliver gate: lacks %q", want)
		}
	}
}

func TestOwnerReviewDocsNameTheGate(t *testing.T) {
	for _, rel := range []string{
		"docs/concepts.md",
		"docs/bundles.md",
		"docs/user-guide.md",
		"docs/design/polyforge-vnext/01-workflow-skills.md",
		"docs/design/polyforge-vnext/reference.md",
	} {
		if !strings.Contains(w149CarrierRead(t, rel), "owner-review") {
			t.Errorf("%s does not describe the owner-review gate", rel)
		}
	}
	for _, rel := range []string{
		"docs/design/polyforge-vnext/01-workflow-skills.md",
		"docs/design/polyforge-vnext/reference.md",
	} {
		if !strings.Contains(w149CarrierRead(t, rel), "`owner_review`") {
			t.Errorf("%s does not name the owner_review shared fragment", rel)
		}
	}
}

// the routing skill keeps both deliver questions human-only, so a session
// agent never answers the owner-review question in the owner's place
func TestOwnerReviewQuestionIsHumanOnlyInTheSkill(t *testing.T) {
	skill := ownerReviewFlat(w149CarrierRead(t, "skills/polyforge/SKILL.md"))
	if !strings.Contains(skill, "the owner-review question (`deliver-owner-review`) are both human-only") {
		t.Errorf("skills/polyforge/SKILL.md does not make deliver-owner-review human-only")
	}
	intent := ownerReviewFlat(w149CarrierRead(t, "skills/polyforge/verbs/execute-intent.md"))
	if !strings.Contains(intent, "the owner-review question (`deliver-owner-review`) - are human-only") {
		t.Errorf("skills/polyforge/verbs/execute-intent.md does not make deliver-owner-review human-only")
	}
	ship := ownerReviewFlat(w149CarrierRead(t, "skills/polyforge/verbs/ship.md"))
	for _, want := range []string{
		"**human-only** question `deliver-owner-review` before any merge",
		`pf answer <id> --text "过目通过"`,
		`pf answer <id> --text "不通过: <what to change>"`,
		"Only a human answers this question — never answer it on the user's behalf or on your own. 过目通过 is not a merge decision",
		"the work item declared no owner-review artifact or the owner answered 过目通过",
	} {
		if !strings.Contains(ship, want) {
			t.Errorf("skills/polyforge/verbs/ship.md lacks %q", want)
		}
	}
	auto := ownerReviewFlat(w149CarrierRead(t, "workflows/autopilot/workflow.md"))
	if !strings.Contains(auto, "A declared `owner-review:` artifact makes deliver ask the human-only `deliver-owner-review` question first, before any merge") {
		t.Errorf("workflows/autopilot/workflow.md's delivery lane fact omits the owner-review gate")
	}
}
