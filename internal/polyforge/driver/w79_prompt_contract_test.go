package driver

// w79_prompt_contract_test.go: pins the step prompts'
// self-consistency and currency, mechanically.
//
//  1. TestW79DeliverPromptNeverForbidsItsOwnDuties — the deliver prompt
//     is assembled exactly as a dispatch assembles it (the shipped chore
//     bundle's shared deliver definition, the memory duty the driver
//     injects for the workflow's LAST step — the wrap summary only, as a
//     gate deliver declares no recall since — and the
//     delivery contract), and it instructs `pf result submit` and the one
//     `pf remember` wrap summary — so no line of it may forbid running
//     `pf` unless the prohibition is scoped to exactly those commands.
//     Red on the old text (its "Never run `pf`" bullet forbade the
//     prompt's own duties).
//  2. TestW79ReviewNeverBlocksOnMissingPriorExperience — a step's
//     missing `## Prior experience` section is a form gap, never a
//     blocking review finding on its own; the substantive hit (repeating
//     a recalled pitfall without saying why this time differs) stays
//     blocking. Red on the old checklist clause.
//  3. TestW79ShippedWorkflowTextNeutralAndCurrent — no shipped
//     workflows/**.md file carries the retired project-specific or
//     stale literals the sweep removed. Red on the pre-sweep tree.
//  4. TestW79LaneRewindQuotaTextMatchesPerEdgeCharging — every delivery
//     lane's workflow.md states the per gate→target edge charging that
//     transition.go implements (countToEdge: each gate that rewinds to a
//     step carries its own charged slots; light and converging rewinds
//     charge none), autopilot states it has no workflow-level budget,
//     and no shared-quota wording survives. A regression pin of the
//     per-edge charging (green on the tree that already states it; the
//     pre-fix shared-quota wording it forbids is the checked-in history).
//  5. TestW79GrillNeverUnattendedCategoriesInline — the feature-full
//     grill applies its no-down-lane rule for never-unattended work, so
//     the never unattended list is stated inline in its text (the six
//     categories), not as a name with no definition. Red on the pre-fix
//     tree (the list was named, never defined).
//
// The file adds no production code, no driver field and no config key.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w79PFBan matches a prohibition of running `pf` ("never run `pf`",
// "do not use pf", …). A line matching it is legal only when the same
// line also scopes the ban to the commands the prompt itself names.
var w79PFBan = regexp.MustCompile("(?i)(never|do not|don't)\\s+(run|execute|use)\\s+`?pf`?")

// w79PFAllowed matches the scoping: the allowed set named on the same
// line as a prohibition (the scoping phrase, or one of the allowed
// commands themselves).
var w79PFAllowed = regexp.MustCompile("commands this prompt names|`pf result submit`|`pf recall`|`pf memory show`|`pf remember`")

// w79ListItem matches the first line of a markdown list item (a bullet
// or a numbered item, nested ones included).
var w79ListItem = regexp.MustCompile(`^\s*([-*+]|[0-9]+[.)])\s`)

// w79Paragraphs splits a markdown prompt into logical units — paragraphs
// (blank-line separated) and, inside a list, each list item on its own
// ( P7: a whole bullet list joined into one unit let a
// sibling bullet naming `pf result submit` mask an unscoped ban) — with
// each unit's wrapped lines joined into one logical line, so a
// prohibition and its scope are compared however the text is wrapped.
func w79Paragraphs(s string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if w79ListItem.MatchString(line) {
			flush()
		}
		cur = append(cur, strings.TrimSpace(line))
	}
	flush()
	return out
}

// w79UnscopedPFBans answers every logical unit of prompt that prohibits
// running `pf` without, in the same unit, scoping the ban to the
// commands the prompt names.
func w79UnscopedPFBans(prompt string) []string {
	var bad []string
	for _, para := range w79Paragraphs(prompt) {
		if w79PFBan.MatchString(para) && !w79PFAllowed.MatchString(para) {
			bad = append(bad, para)
		}
	}
	return bad
}

// TestW79DeliverPromptNeverForbidsItsOwnDuties: the assembled deliver
// prompt names the pf commands it instructs (the delivery contract's
// `pf result submit`, the last step's one `pf remember` wrap summary)
// and never prohibits `pf` unscoped — the prompt must not contradict
// itself.
func TestW79DeliverPromptNeverForbidsItsOwnDuties(t *testing.T) {
	bundle := filepath.Join(shippedWorkflowsDir, "chore")
	capDir := t.TempDir()
	writeCapsuleFile(t, capDir, capsule.WorkItemJSON,
		`{"id":"wi_w79","slug":"demo#1","project":"polyforge","goal":"g"}`)
	def, err := readStepDefinition(bundle, "deliver", nil)
	if err != nil {
		t.Fatalf("read the shipped deliver definition: %v", err)
	}
	prompt := assembleStepPrompt(promptParts{
		WIID: "wi_w79", StepID: "deliver", SpawnN: 1,
		CapsuleRoot: "/ws/capsules/w79",
		ContextMode: ContextFresh,
		Definition:  def,
		Gate:        true,
		// deliver is the chore workflow's last step, so the memory duty
		// carries the wrap summary (and, a gate declaring no recall, no
		// recall duty); its step entry declares the command evidence gate.
		RequireCommandEvidence: true,
		MemoryDuty:             memoryDutySection(bundle, capDir, false, true),
	})
	// (a) the prompt instructs every command the scoped ban allows.
	for _, want := range []string{
		"pf result submit",
		"pf remember --project polyforge",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the assembled deliver prompt must instruct %q — the scoped pf ban allows exactly the commands it names", want)
		}
	}
	// (b) no paragraph or list item prohibits pf unless it also names the
	// allowed set.
	for _, para := range w79UnscopedPFBans(prompt) {
		t.Errorf("the deliver prompt prohibits pf without scoping the ban to the commands it instructs (pf result submit, the one pf remember wrap summary):\n%s", para)
	}
	// (c) the check has teeth ( P7): an unscoped ban
	// reintroduced as its own bullet of the real hard-rules list — right
	// beside the bullet that names `pf result submit` — is caught.
	const scoped = "- `pf` is limited to the commands this prompt names"
	if !strings.Contains(prompt, scoped) {
		t.Fatalf(" P7: the deliver hard rules lost their scoped pf bullet %q — the mutation below has no anchor", scoped)
	}
	mutated := strings.Replace(prompt, scoped, "- Never run `pf`.\n"+scoped, 1)
	if bad := w79UnscopedPFBans(mutated); len(bad) != 1 || !strings.HasPrefix(bad[0], "- Never run `pf`.") {
		t.Errorf(" P7: an unscoped \"Never run `pf`\" bullet beside the scoped one must be the one finding, got %q", bad)
	}
}

// TestW79PFBanCheckSplitsListItems ( P7): each list item is
// its own unit, so a sibling bullet naming an allowed command never masks
// an unscoped ban, while a ban scoped inside its own item stays legal.
func TestW79PFBanCheckSplitsListItems(t *testing.T) {
	unscoped := "## Hard rules\n\n- Never run `pf`.\n- Deliver the envelope with\n  `pf result submit`.\n1. Do not use pf here.\n2. Then run `pf recall`.\n"
	if bad := w79UnscopedPFBans(unscoped); len(bad) != 2 {
		t.Errorf(" P7: want both unscoped bans (a bullet and a numbered item) found despite their scoped siblings, got %q", bad)
	}
	// The joined-list reading this replaces would have masked both:
	// blank-line paragraphs alone put each ban beside its allowed sibling.
	for _, para := range strings.Split(unscoped, "\n\n") {
		if w79PFBan.MatchString(para) && !w79PFAllowed.MatchString(para) {
			t.Fatalf(" P7: the fixture no longer exercises the masking (a blank-line paragraph alone already catches it):\n%s", para)
		}
	}
	scoped := "- Never run `pf` beyond the commands this prompt names,\n  in particular no state-changing verb.\n- Other rule.\n"
	if bad := w79UnscopedPFBans(scoped); len(bad) != 0 {
		t.Errorf(" P7: a ban scoped inside its own (wrapped) item is legal, got %q", bad)
	}
}

// TestW79ReviewNeverBlocksOnMissingPriorExperience: the owner-rules
// checklist's "Prior experience ignored" bullet blocks on the
// substantive hit only; the findings guidance states a missing
// `## Prior experience` section is a form gap, never a blocking finding
// on its own.
func TestW79ReviewNeverBlocksOnMissingPriorExperience(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "review.md"))
	if err != nil {
		t.Fatalf("read the shared review definition: %v", err)
	}
	s := string(raw)
	checklist := w79Section(s, "## Owner-rules checklist")
	if checklist == "" {
		t.Fatal("the shared review definition has no Owner-rules checklist section")
	}
	if strings.Contains(checklist, "skipped its `## Prior experience` section") {
		t.Error("the owner-rules checklist still blocks on a missing ## Prior experience section — a form gap is never a blocking finding on its own")
	}
	if !strings.Contains(checklist, "**Prior experience ignored.**") {
		t.Error("the owner-rules checklist lost the substantive Prior experience hit (repeating a recalled pitfall without saying why this time differs stays blocking)")
	}
	// Whitespace-normalized so the sentence matches however it wraps.
	flat := strings.Join(strings.Fields(s), " ")
	if !strings.Contains(flat, "is never a blocking finding on its own") {
		t.Error("the review findings guidance must state a missing ## Prior experience section is never a blocking finding on its own (record it non-blocking if noted at all)")
	}
}

// w79Section answers the body under a "## <heading>" up to the next H2,
// or "" when the heading is absent.
func w79Section(s, heading string) string {
	i := strings.Index(s, heading)
	if i < 0 {
		return ""
	}
	rest := s[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// w79RetiredLiterals are the project-specific or stale literals the
// sweep retired from workflows/: the owner-kept/V4 pre-flight wording,
// the formerly-worded verdict parentheticals and the 01-workflow-skills
// design-doc citation, the verbs/*.md skill-path citations, the
// nonexistent autopilot 90-minute budget, the session-continuation
// claim (the mechanism is a handoff file), and the e2e-author mode's
// constant explanation.
var w79RetiredLiterals = []string{
	"owner-kept",
	"V4",
	"formerly worded",
	"01-workflow-skills",
	"verbs/",
	"90-minute budget",
	"continues `e2e-author`'s session",
	"static default",
}

// TestW79ShippedWorkflowTextNeutralAndCurrent: no shipped workflows/**.md
// file carries a retired literal — workflows/ stays project-neutral and
// current (the filing-skill definitions and design docs the retired
// paths pointed at still exist; only the lane text stops naming them).
func TestW79ShippedWorkflowTextNeutralAndCurrent(t *testing.T) {
	files := w79ShippedWorkflowTexts(t)
	if len(files) == 0 {
		t.Fatal("no shipped workflow text found")
	}
	for _, f := range files {
		for _, lit := range w79RetiredLiterals {
			if strings.Contains(f.text, lit) {
				t.Errorf("%s carries the retired literal %q — workflows/ stays project-neutral and current", f.path, lit)
			}
		}
	}
}

// TestW79LaneRewindQuotaTextMatchesPerEdgeCharging: every delivery
// lane's workflow.md states the per gate→target edge charging
// (transition.go's countToEdge: the per-step bound is not one pool the
// gates share — each gate→target edge carries its own charged slots, and
// light and converging rewinds charge none), autopilot states it has no
// workflow-level budget, and no shipped workflow text carries a
// shared-quota claim.
func TestW79LaneRewindQuotaTextMatchesPerEdgeCharging(t *testing.T) {
	lanes := map[string]bool{"chore": true, "bug-fix": true, "autopilot": true, "feature-full": true}
	entries, err := os.ReadDir(shippedWorkflowsDir)
	if err != nil {
		t.Fatalf("read workflows/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := filepath.Join(shippedWorkflowsDir, e.Name(), "workflow.md")
		raw, err := os.ReadFile(rel)
		if err != nil {
			continue // a directory with no workflow.md is not a bundle
		}
		s := string(raw)
		if lanes[e.Name()] && !strings.Contains(s, "per gate→target edge") {
			t.Errorf("%s must state the per gate→target edge charging — transition.go charges the per-step bound per gate→target edge (countToEdge), not as one pool the gates share", rel)
		}
		if e.Name() == "autopilot" && !strings.Contains(s, "No workflow-level budget") {
			t.Errorf("%s must state it has no workflow-level budget (the bounds are the rewind limits and the daemon's per-step runtime bound)", rel)
		}
	}
	// No shipped workflow text may carry the pre-per-edge shared-quota
	// wording (the retired claim: one allowance the gates draw on
	// together).
	for _, f := range w79ShippedWorkflowTexts(t) {
		for _, shared := range []string{"is shared by the", "shared across the", "each gate 3 times"} {
			if strings.Contains(f.text, shared) {
				t.Errorf("%s still states a shared per-step rewind quota (%q) — the bound is charged per gate→target edge", f.path, shared)
			}
		}
	}
}

// TestW79GrillNeverUnattendedCategoriesInline: the feature-full grill
// keys its no-down-lane rule on the never unattended list, so the list
// is stated inline where the rule is applied — its six categories, not
// a name whose definition lives in a doc the adopting project may not
// carry.
func TestW79GrillNeverUnattendedCategoriesInline(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, "feature-full", "steps", "grill.md"))
	if err != nil {
		t.Fatalf("read the shipped feature-full grill definition: %v", err)
	}
	// Whitespace-normalized so a category matches however it wraps.
	flat := strings.Join(strings.Fields(string(raw)), " ")
	if !strings.Contains(flat, "never unattended") {
		t.Fatal("the feature-full grill lost the `never unattended` marker its no-down-lane rule keys on")
	}
	for _, cat := range []string{
		"release/deploy work",
		"schema or data migration",
		"credentials and permission surface",
		"removing features or weakening guarantees",
		"security-related changes",
		"large cross-repo changes",
	} {
		if !strings.Contains(flat, cat) {
			t.Errorf("the feature-full grill must state the never unattended category %q inline — the no-down-lane rule is applied from the step text alone", cat)
		}
	}
}

type w79File struct {
	path string
	text string
}

// w79ShippedWorkflowTexts answers every shipped workflows/**.md file's
// path and content.
func w79ShippedWorkflowTexts(t *testing.T) []w79File {
	t.Helper()
	var out []w79File
	err := filepath.WalkDir(shippedWorkflowsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, w79File{path: path, text: string(raw)})
		return nil
	})
	if err != nil {
		t.Fatalf("walk workflows/: %v", err)
	}
	return out
}
