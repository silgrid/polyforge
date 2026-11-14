package test

// owner_review_gate_contract_test.go: text contract — a
// work item that declares an owner-review artifact has it shown to the owner
// in publish's question (`pf step ask`, workflow contract 4) once its PRs
// are open, and the lane's
// merge step merges only after the owner answers; a work item that declares
// none is asked nothing.
//
// The declaration duty lives once, in the shared fragment
// workflows/_shared/owner_review.md, included by the shipped build steps
// (develop's build, micro's fix); the gate itself is a section of the shared
// publish step. Red on the pre-change tree: the fragment, the gate section
// and the includes do not exist there.

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// the shipped (bundle, step) pairs that may declare an owner-review
// artifact (micro's fix, the lane has no plan, so its build
// step's hand-off body is the only place to declare one; the retired lanes'
// plan and build steps were removed)
var ownerReviewAuthoringSteps = [][2]string{
	{"micro", "fix"},
	{"develop", "build"}, // the develop lane's one authoring step
}

// the fragment's operative lines (compared whitespace-flattened); each
// renders exactly once per including step
var ownerReviewDutyLines = []string{
	"## Owner review before merge",
	"`publish` step's owner-review gate",
	"owner-review: <capsule-relative path>[, <path>...] — <one-line description>",
	"That line is the only switch",
	"publish then asks the owner (`pf step ask`) once the PRs are open, before the lane's `merge` step merges them",
	"「过目通过」",
	"shown read-only and never edited for the review",
	"never declare one for material no human needs to judge",
}

var ownerReviewPlaceholder = regexp.MustCompile(`\{\{[^}]*\}\}`)

// ownerReviewFlat collapses every whitespace run to one space, so a
// re-wrapped paragraph keeps matching
func ownerReviewFlat(s string) string { return strings.Join(strings.Fields(s), " ") }

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
		out, err := workflow.RenderDefinition(filepath.Join(v1Root(t), "workflows", bundle), step, nil)
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
			t.Errorf("%s includes {{owner_review}} but is not one of the authoring steps", rel)
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

// the routing skill keeps the deliver question human-only, so a session
// agent never answers the owner-review question in the owner's place (the
// micro resolved-conflict question went with micro's daemon sync, workflow
// contract 2)
func TestOwnerReviewQuestionIsHumanOnlyInTheSkill(t *testing.T) {
	skill := ownerReviewFlat(w149CarrierRead(t, "skills/polyforge/SKILL.md"))
	if !strings.Contains(skill, "never answer the publish step's owner-review question (its `pf step ask`) yourself: it is human-only") {
		t.Errorf("skills/polyforge/SKILL.md does not make publish-owner-review human-only")
	}
	intent := ownerReviewFlat(w149CarrierRead(t, "skills/polyforge/verbs/execute-intent.md"))
	if !strings.Contains(intent, "the publish step's owner-review question (its `pf step ask`) is human-only") {
		t.Errorf("skills/polyforge/verbs/execute-intent.md does not make publish-owner-review human-only")
	}
	ship := ownerReviewFlat(w149CarrierRead(t, "skills/polyforge/verbs/ship.md"))
	for _, want := range []string{
		"the **human-only** owner-review question (its `pf step ask`, no question id) before any merge",
		`pf answer <id> --text "过目通过"`,
		`pf answer <id> --text "不通过: <what to change>"`,
		"Only a human answers this question — never answer it on the user's behalf or on your own. 过目通过 is not a merge decision",
		"the work item declared no owner-review artifact or the owner answered 过目通过",
	} {
		if !strings.Contains(ship, want) {
			t.Errorf("skills/polyforge/verbs/ship.md lacks %q", want)
		}
	}
}
