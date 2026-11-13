package driver

// w79_prompt_contract_test.go: pins the step prompts'
// self-consistency and currency, mechanically.
//
//  1. TestW79DeliverPromptNeverForbidsItsOwnDuties — the deliver prompt
//     is assembled exactly as a dispatch assembles it (the shipped micro
//     bundle's shared deliver definition, the memory duty the driver
//     injects for the workflow's LAST step — the wrap summary only, as a
//     gate deliver declares no recall — and the
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
//  4. TestW79LaneRewindTextMatchesMaxRewinds — every shipped lane that
//     declares a rewind budget states it as max_rewinds, the one
//     lane-wide budget (contract 1), and no text names the removed
//     max_total / max_per_step bounds or a shared-quota claim.
//
// (A fifth pin, the feature-full grill's never-unattended categories,
// went with that lane,/#428.)
//
// The file adds no production code, no driver field and no config key.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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
// (P7: a whole bullet list joined into one unit let a
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

// TestW79PFBanCheckSplitsListItems (P7): each list item is
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
		t.Error("the owner-rules checklist lost the substantive Prior experience hit (repeating a recalled pitfall without saying why this time differs stays on the checklist, blocking when it is a defect —'s `bug` class)")
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

// TestW79LaneRewindTextMatchesMaxRewinds: every shipped lane whose
// workflow.yaml declares a rewind budget states it as the one lane-wide
// budget, `max_rewinds` (contract 1: the per-edge pair is gone), and no
// shipped workflow text still names the removed `max_total` /
// `max_per_step` bounds or a shared-quota claim.
func TestW79LaneRewindTextMatchesMaxRewinds(t *testing.T) {
	bounded := 0
	entries, err := os.ReadDir(shippedWorkflowsDir)
	if err != nil {
		t.Fatalf("read workflows/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := filepath.Join(shippedWorkflowsDir, e.Name(), "README.md")
		raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, e.Name(), workflow.WorkflowFile))
		if err != nil {
			continue // a directory with no workflow.yaml is not a lane
		}
		readme, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("lane %s has no README.md: %v", e.Name(), err)
		}
		front, body := string(raw), string(readme)
		if !strings.Contains(front, "max_rewinds:") {
			continue // no rewind edge (integrate, release)
		}
		bounded++
		if !strings.Contains(body, "max_rewinds") {
			t.Errorf("%s must state its rewind budget as max_rewinds in its narrative", rel)
		}
	}
	if bounded == 0 {
		t.Fatal("no shipped lane declares max_rewinds — the pin checks nothing (develop and micro declare one)")
	}
	for _, f := range w79ShippedWorkflowTexts(t) {
		for _, stale := range []string{"max_per_step", "max_total", "is shared by the", "shared across the", "each gate 3 times"} {
			if strings.Contains(f.text, stale) {
				t.Errorf("%s still names %q — the lane's one rewind budget is max_rewinds", f.path, stale)
			}
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
		if ext := strings.ToLower(filepath.Ext(path)); d.IsDir() || (ext != ".md" && ext != ".yaml") {
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
