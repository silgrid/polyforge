// carries: workflows/integrate/workflow.yaml, workflows/integrate/steps/repair.md, workflows/integrate/steps/review.md, workflows/integrate/skills/repair.md, internal/contracttest/w98_batch_repair_text_contract_test.go

package test

// w98_batch_repair_text_contract_test.go (polyforge#98): the integration
// lane's in-batch repair. The lane's `steps:` list is the only execution
// order, so the shape is pinned where it lives: two `rewind_only` steps
// (the repair step and its review) sit between collect and the reverts,
// the review gates the repair (its FAIL rewinds there), and the full
// gate's on_fail target is the repair step — the gate's red, once every
// red unit is attributed and fixable by touching only tests or
// documentation, hands the batch back there instead of filing a fix
// ticket, and the fix lands as a PR the revert step merges under the
// merge lock. The step id is never `fix` (a workspace's per-step model
// key of that name governs another lane's bounds step), and the new
// step definition and skill texts name no project, component, lane or
// ticket (workflows/ stays project-neutral, the pf62 rule). The texts
// are read through the yaml where a key's presence is the fact, never
// as a substring, so a key of another name cannot satisfy the pin.
//
// Red on the base tree: the lane carries four steps and neither the
// repair step nor its review exists — the first order assertion fails.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestW98' -count=1 -v

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// w98Step is one step of the integration lane's yaml as this contract
// reads it.
type w98Step struct {
	Id         string `yaml:"id"`
	Skill      string `yaml:"skill"`
	Gate       bool   `yaml:"gate"`
	RewindOnly bool   `yaml:"rewind_only"`
	ModelTier  string `yaml:"model_tier"`
	OnFail     *struct {
		Target string `yaml:"target"`
	} `yaml:"on_fail"`
}

// w98Lane reads the integration lane's workflow.yaml.
func w98Lane(t *testing.T) (steps []w98Step, maxRewinds int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), "workflows", "integrate", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MaxRewinds int       `yaml:"max_rewinds"`
		Steps      []w98Step `yaml:"steps"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("workflows/integrate/workflow.yaml does not parse: %v", err)
	}
	return doc.Steps, doc.MaxRewinds
}

// TestW98BatchRepairLaneShape: the lane's own steps list carries the
// repair route — collect, the repair step, its review, the reverts, the
// full gate, close — with the two new steps rewind_only, the review a
// gate over the repair, and the full gate's FAIL rewinding to the repair
// step; no step id is `fix`; the lane's one rewind budget stays 3.
func TestW98BatchRepairLaneShape(t *testing.T) {
	steps, maxRewinds := w98Lane(t)
	var order []string
	by := map[string]w98Step{}
	for _, s := range steps {
		order = append(order, s.Id)
		by[s.Id] = s
	}
	if got := strings.Join(order, ","); got != "collect,repair,review,revert,full-gate,close" {
		t.Fatalf("the lane's order must be collect → repair → review → revert → full-gate → close, got %v", got)
	}
	if maxRewinds != 3 {
		t.Errorf("max_rewinds %d, want 3 (the repair rounds share the lane's one budget)", maxRewinds)
	}
	for _, id := range []string{"repair", "review", "revert", "full-gate", "close"} {
		if strings.Contains(id, "fix") {
			t.Fatalf("the step id %q is the bounds step's name another lane's model key governs", id)
		}
	}
	r, ok := by["repair"]
	if !ok {
		t.Fatal("no step repair")
	}
	if !r.RewindOnly {
		t.Errorf("repair declares no rewind_only: its first round must be skipped — it runs only after the gate's FAIL covers it")
	}
	if r.Gate || r.OnFail != nil {
		t.Errorf("repair is no gate (it hands off done, or asks the owner): gate %v on_fail %+v", r.Gate, r.OnFail)
	}
	if r.Skill != "repair" {
		t.Errorf("repair declares skill %q, want repair (the lane's own skill)", r.Skill)
	}
	if r.ModelTier != "" {
		t.Errorf("repair declares model_tier %q: it runs on its own per-step key, else the project default, like a build session", r.ModelTier)
	}
	v, ok := by["review"]
	if !ok {
		t.Fatal("no step review")
	}
	if !v.RewindOnly {
		t.Errorf("review declares no rewind_only: its first round must be skipped with the repair's")
	}
	if !v.Gate || v.OnFail == nil || v.OnFail.Target != "repair" {
		t.Errorf("review must gate the repair (gate: true, on_fail target repair), got gate %v on_fail %+v", v.Gate, v.OnFail)
	}
	g, ok := by["full-gate"]
	if !ok {
		t.Fatal("no step full-gate")
	}
	if !g.Gate || g.OnFail == nil || g.OnFail.Target != "repair" {
		t.Errorf("the full gate's on_fail must target repair (a red the batch can fix in-batch rewinds there), got gate %v on_fail %+v", g.Gate, g.OnFail)
	}
}

// w98NoNames is what the new step texts may not carry — a project,
// component, lane or ticket identifier (the pf62 rule, extended to the
// integration lane's new texts: workflows/ stays project-neutral).
var w98NoNames = regexp.MustCompile(`polyforge|pi-web|spec-superflow|e2e-parallel|develop lane|micro lane|#[0-9]`)

// TestW98NewStepTextsNameNoProject (AC7): the repair step's definition,
// the review's and the repair skill carry no project, component, lane or
// ticket name, and each of the two definitions carries the two sections
// every step text owes (what it reads before it starts, what its
// hand-off carries).
func TestW98NewStepTextsNameNoProject(t *testing.T) {
	rel := []string{
		"workflows/integrate/steps/repair.md",
		"workflows/integrate/steps/review.md",
		"workflows/integrate/skills/repair.md",
	}
	for _, p := range rel {
		raw, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(p)))
		if err != nil {
			t.Fatalf("%s: %v (the lane's new texts ship with the release)", p, err)
		}
		text := string(raw)
		if bad := w98NoNames.FindString(text); bad != "" {
			t.Errorf("%s names %q — workflows/ stays project-neutral", p, bad)
		}
		if strings.Contains(p, "skills/") {
			continue // the skill's sections are the shared skill contract's to check
		}
		flat := strings.Join(strings.Fields(text), " ")
		for _, heading := range []string{"before you start: what you read and check", "what your hand-off carries"} {
			if !strings.Contains(flat, strings.Join(strings.Fields(heading), " ")) {
				t.Errorf("%s carries no section %q", p, heading)
			}
		}
	}
}

// w98Flat reads a repository file with its whitespace folded (the w52
// rule: a rewrap keeps the pin).
func w98Flat(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return strings.Join(strings.Fields(string(raw)), " ")
}

// TestW98FullGateRepairRouteText (AC1, AC2, AC5, AC6): the gate's own
// routing text — the red it hands back to the repair step (the brief's
// schema, the charged FAIL, the human-merge read that switches the route
// off, the refused repair it reads back), and the owner's revert
// rewinding to the revert step by name. Whitespace-flattened, so a
// rewrap keeps them pinned.
func TestW98FullGateRepairRouteText(t *testing.T) {
	flat := w98Flat(t, "workflows/integrate/skills/full-gate.md")
	for _, want := range []string{
		// the route's conditions: every red unit attributed, no source
		// past two fix rounds, and the fix of every unit touching only
		// tests or documentation
		"the fix of every unit touches only tests or documentation",
		// a project whose PRs await a person merges none by machine: the
		// frozen merge mode switches the route off (AC6)
		"the frozen step modes (`runtime-state/step-modes.json`) name `merge` `human`",
		"a project whose PRs await a person merges none by machine: the fix tickets it is",
		// the brief's schema, written before the FAIL
		"`artifacts/integrate/repair.json`: `{\"round\": <$PF_ROUND, a number>, \"heads\": {\"<repo>\": \"<sha>\"}, \"units\": [{\"repo\", \"unit\", \"item\", \"file\", \"log\", \"excerpt\", \"rerun\"}]}`",
		// the charged FAIL — never the owner's
		"hand off `--status fail` — charged, never `--per-owner`",
		// the owner's revert names the revert step: the gate's own FAIL
		// would rewind to the repair step (AC5)
		"hand off `--status rewind --to revert --per-owner <the reply's at>`",
		// a repair the owner refused is never taken again: the next round
		// files the fix tickets (AC2)
		"`artifacts/integrate/repair-refused.json`",
	} {
		if !strings.Contains(flat, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("workflows/integrate/skills/full-gate.md must state %q", want)
		}
	}
}

// TestW98RepairFreshnessAndWaysOut (review round 1, #1 and #3): the
// repair route's freshness never compares a file's round with a step's
// own — rounds are per step, so the brief carries the gate's round and
// the plan and the refusal carry the brief round they answer — and the
// repair's out-of-scope ways out are shapes the daemon accepts: the
// owner-ruled revert rides the gate decision's reverts with a plain
// done (a rewind to a later step the call always refuses), and a refused
// or reverted brief leaves no plan behind to land.
func TestW98RepairFreshnessAndWaysOut(t *testing.T) {
	repair := w98Flat(t, "workflows/integrate/skills/repair.md")
	for _, want := range []string{
		// #1: the freshness keys on the brief's round, never the step's
		// own (a review FAIL or an owner's rewind-to-revert shifts the
		// steps' rounds apart)
		"The brief's `round` is the gate's, never yours — no comparison with your own round decides anything",
		// the plan and the refusal answer a brief, not a step round
		"`\"brief_round\": <the brief's round>`",
		"an existing plan answering this very brief is kept: check its branch out as it stands (never `checkout -B`, never rewritten history)",
		// #3: the owner-ruled revert rides the decision and a plain done
		"write the item into `artifacts/integrate/gate-decision.json`'s `reverts`",
		"remove the repair plan file",
		"then hand off `done`",
	} {
		if !strings.Contains(repair, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("workflows/integrate/skills/repair.md must state %q", want)
		}
	}
	if strings.Contains(repair, "--status rewind") {
		t.Errorf("workflows/integrate/skills/repair.md hands off a rewind — to the later step the daemon always refuses it (review r1 #3)")
	}
	// the review judges the plan answering the brief, and passes with
	// nothing that does
	review := w98Flat(t, "workflows/integrate/steps/review.md")
	for _, want := range []string{
		"whose `brief_round` equals the brief's `round`",
		"the refusal of this brief, a plan answering an older brief, or no plan at all: nothing to judge",
	} {
		if !strings.Contains(review, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("workflows/integrate/steps/review.md must state %q", want)
		}
	}
	// the revert step reads the reverts the repair step's ruling wrote
	// beside the gate's
	revertSkill := w98Flat(t, "workflows/integrate/skills/revert.md")
	if !strings.Contains(revertSkill, "the repair step's owner-ruled revert of a source the batch cannot fix in place") {
		t.Errorf("workflows/integrate/skills/revert.md must name the repair step's reverts as a source of its list")
	}
	// #N5: a red the dependency audit raised is never the batch's
	fg := w98Flat(t, "workflows/integrate/skills/full-gate.md")
	if !strings.Contains(fg, "a red the dependency audit raised is never the batch's") {
		t.Errorf("workflows/integrate/skills/full-gate.md must exclude the audit red from the repair route")
	}
}
