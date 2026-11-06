package driver

// w425_build_subagents_test.go: the develop build
// delegates the analysis — grill, defect reproduction, the plan — to one
// fresh-context sub-agent and, past a write-set threshold, every unit to
// implementer sub-agents, under either harness (Claude's Agent tool,
// pi's subagent tool); the owner's questions stay this session's one
// batched stop. The rules live once, in workflows/_shared/build_subagents.md,
// rendered into the build prompt; the threshold is written once in the
// whole workflow definition.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

func TestDevelopBuildPromptDelegatesToSubagents(t *testing.T) {
	frag, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "build_subagents.md"))
	if err != nil {
		t.Fatal(err)
	}
	def, err := readStepDefinition(filepath.Join(shippedWorkflowsDir, developBundle), "build", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(def, strings.TrimSpace(string(frag))); n != 1 {
		t.Fatalf("develop/build renders the build_subagents fragment %d times, want exactly 1", n)
	}
	p := spawnStepPrompt(t, developBundle, "build")
	flat := strings.Join(strings.Fields(p), " ")
	for _, w := range []string{
		// AC1: the analysis is the sub-agent's; the stop stays this session's
		// (that it reproduces a defect and writes the plan, does not grill and
		// that the session asks the owner are the fragment's lines
		// test/w403_develop_grill_e2e_contract_test.go pins,)
		"## Sub-agents",
		"**The analysis sub-agent**",
		"every surprise as one question with its options and a recommended answer — a dead end, a ticket that contradicts the code or itself, a plan that diverges from the ticket",
		// a ticket without an acceptance section: the sub-agent proposes the list, the owner replies to it
		"A work item without an acceptance section: the sub-agent proposes the list in the plan's `## Acceptance` and returns it, and plans the units against it.",
		"and folding an owner's answer into the plan is its too.",
		"Item 2 below, and reproducing and locating a reported defect, are the analysis sub-agent's",
		"in the capsule's `workflows/_rendered/build.md`",
		// AC2: the big plan, the grouping, the parallel bound
		"Count the distinct paths the plan's `## Units` declare `(write)`, each path once across all units.",
		"every unit is built by a fresh-context implementer sub-agent, test first, and you only orchestrate;",
		"you build every unit yourself.",
		// units are built in plan order; only independent tasks overlap
		"A task carries consecutive units of the plan, and the units are built in plan order: a task starts only once every earlier unit has finished",
		"except that tasks whose write sets are disjoint, each working in a different repo clone and none needing another task's code, may run at the same time",
		"An implementer's instruction is this section's per-unit text in that file",
		// AC3: the record
		"A unit is `subagent` when an implementer sub-agent made both its commits, else `main`",
		// AC4: Claude dispatches through Agent; a dispatch without an
		// isolated sub-agent (pi's fact, a Claude step without the Agent tool) builds itself
		"The `Agent` tool (Claude Code), `subagent_type: \"general-purpose\"`, in the foreground (never `run_in_background`)",
		// (contract 3's `summary` is the hand-off since workflow contract 4)
		"When this dispatch's facts say it dispatches no sub-agent, your tools offer no `Agent` tool, or a dispatch fails to start, do that work yourself, record `main` and say why in your hand-off.",
		// a task is self-contained and never stops the step (workflow
		// contract v2 PR 12: the step's own asks are the session's; since
		// contract 4 every owner question, hand-off and hub call is a
		// `pf step` the task never runs, and contract 3's result envelope
		// is artifacts/handoff.md)
		"It never runs `pf step` (no question to the owner, no hand-off, no hub call), never writes `runtime-state/` or `artifacts/handoff.md`",
		// PR 12: a task returns its unit's run; the session records units.json
		"the run that went red then green (`repo`, `cwd`, `argv`) with both exit codes, its marker",
		"You write `artifacts/build/units.json` from what the tasks return (a task never writes it).",
	} {
		if !strings.Contains(flat, w) {
			t.Errorf("develop/build prompt must carry %q", w)
		}
	}
	if strings.Contains(p, "{{") {
		t.Error("develop/build prompt carries an unrendered placeholder")
	}
}

// AC2: the write-set threshold is written in exactly one place of the
// workflow definition (the develop bundle and the shared fragments), as
// one "More than N" with its "N or fewer" complement.
func TestBuildSubagentThresholdWrittenOnce(t *testing.T) {
	more := regexp.MustCompile(`(?i)\bmore than ([0-9]+)\b`)
	fewer := regexp.MustCompile(`(?i)\b([0-9]+) or fewer\b`)
	type hit struct{ file, n string }
	var mores, fewers []hit
	for _, root := range []string{filepath.Join(shippedWorkflowsDir, developBundle), filepath.Join(shippedWorkflowsDir, workflow.SharedDir)} {
		err := filepath.WalkDir(root, func(p string, de os.DirEntry, err error) error {
			if err != nil || de.IsDir() || !strings.HasSuffix(p, ".md") {
				return err
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			flat := strings.Join(strings.Fields(string(raw)), " ")
			for _, m := range more.FindAllStringSubmatch(flat, -1) {
				mores = append(mores, hit{filepath.Base(p), m[1]})
			}
			for _, m := range fewer.FindAllStringSubmatch(flat, -1) {
				fewers = append(fewers, hit{filepath.Base(p), m[1]})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(mores) != 1 || len(fewers) != 1 || mores[0] != fewers[0] || mores[0].file != "build_subagents.md" {
		t.Fatalf("the threshold must be written once, in build_subagents.md: more-than %v, or-fewer %v", mores, fewers)
	}
}

// PR #869 review, blocking 2 (route c): a pi sub-agent does not run with
// its step's isolation — the subagent extension starts the worker as
// `pi --mode json -p --no-session …` without the step's
// --no-context-files/--no-skills/--no-prompt-templates, and pi offers no
// environment or settings switch for context files — so a pi dispatch of
// a step that carries the sub-agent rules is told, as a driver fact, that
// it dispatches none and records `main`; a Claude dispatch is not.
func TestPiBuildDispatchIsToldSubagentsAreUnavailable(t *testing.T) {
	// workflow contract 4: the fact is one of part 1's dispatch facts
	const fact = "This dispatch runs on pi, whose sub-agents do not run with this step's isolation"
	pi := spawnStepPromptWith(t, developBundle, StepSpawn{StepID: "build", SpawnN: 1, Harness: "pi"})
	flat := strings.Join(strings.Fields(pi), " ")
	for _, w := range []string{
		fact,
		"Dispatch no sub-agent: do the analysis and build every unit yourself, record each unit's `implemented_by` in `artifacts/build/units.json` as `main`",
	} {
		if !strings.Contains(flat, w) {
			t.Errorf("a pi build dispatch must carry %q", w)
		}
	}
	if i := strings.Index(pi, fact); i < 0 || i > strings.Index(pi, "# Prohibited actions") {
		t.Error("the fact is part 1's, before the prohibited actions")
	}
	if p := spawnStepPromptWith(t, developBundle, StepSpawn{StepID: "build", SpawnN: 1, Harness: "claude"}); strings.Contains(p, fact) {
		t.Error("a claude build dispatch carries no such fact")
	}
	if p := spawnStepPromptWith(t, developBundle, StepSpawn{StepID: reviewStepID, SpawnN: 1, Harness: "pi"}); strings.Contains(p, fact) {
		t.Error("a step without the sub-agent rules carries no such fact")
	}
}
