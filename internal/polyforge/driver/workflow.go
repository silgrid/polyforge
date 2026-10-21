package driver

// workflow.go: the ONE parse of a bundle's workflow.md frontmatter. The
// driver (sequencing, supervision overrides, result duties) and the
// spawn adapter (prompt assembly) read the same Workflow value, parsed
// once in driver.New / NewStepSpawnAdapter. The read is loose (unknown
// keys ignored); validateWorkflow is the one structural check of a
// bundle (docs/design/polyforge-vnext/01-workflow-skills.md §4) and
// surfaces every parse error.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"gopkg.in/yaml.v3"
)

const (
	defaultMaxRewindTotal   = 4
	defaultMaxRewindPerStep = 2
)

// Context policies (the frontmatter extension vocabulary).
const (
	// ContextInherit: the step continues the previous step's work —
	// its prompt carries the producing step's handoff artifact.
	ContextInherit = "inherit"
	// ContextFresh: the step starts from a clean context — declared
	// inputs only, never a handoff.
	ContextFresh = "fresh"
)

// Workflow is the execution graph: the step order IS the declaration
// order (rule V3), plus the rewind bounds.
type Workflow struct {
	Steps      []StepSpec
	MaxTotal   int
	MaxPerStep int
}

// StepSpec is one workflow step: identity and order, gate policy,
// context policy and the artifact surface the spawn adapter assembles
// prompts from. Candidate lists are not read here: the claim composes
// them (Config.StepCandidates).
type StepSpec struct {
	ID   string
	Gate bool
	// ContextPolicy is ContextInherit (the default) or ContextFresh.
	ContextPolicy string
	OnFail        *OnFail
	// Handoff marks a step that must produce a handoff artifact
	// (artifacts/handoff-<id>.md) for a later inherit step; the prompt
	// carries the duty, nothing checks the file (resolveHandoff).
	Handoff bool
	// RequiresHumanAnswer mirrors `requires_human_answer: true`: the step
	// may not complete until a human answered at least one of its
	// questions in the current attempt chain (ResultSpec.HumanAnswers).
	RequiresHumanAnswer bool
	// HumanAnswerIfAsked mirrors `requires_human_answer: if_asked`
	// (polyforge#164 H11): the step's questions are a human's to answer
	// (the daemon refuses an answer from a step process tree), and a
	// completion with ZERO human answers is accepted but recorded as the
	// durable EvHumanGateUnasked event, which notifies the owner.
	HumanAnswerIfAsked bool
	// RequiresCommandEvidence mirrors `requires_command_evidence: true`
	// (the V4 gate).
	RequiresCommandEvidence bool
	// The step's wall-budget estimate (budget.wall_minutes, T's base).
	WallMinutes int
	// Vars fills the {{name}} placeholders of the step's definition file
	// (workflow.RenderStepVars): the lane-specific facts a shared
	// workflows/_shared/steps/<id>.md leaves to each bundle.
	Vars map[string]string
}

// OnFail is the gate FAIL policy: the rewind target the gate's FAIL
// verdict rewinds to (the terminate option is deleted — a gate FAIL
// always rewinds to its declared target).
type OnFail struct {
	Target string
}

func (w *Workflow) indexOf(stepID string) (int, bool) {
	for i, s := range w.Steps {
		if s.ID == stepID {
			return i, true
		}
	}
	return -1, false
}

// step answers the step spec for id (nil when absent).
func (w *Workflow) step(id string) *StepSpec {
	if i, ok := w.indexOf(id); ok {
		return &w.Steps[i]
	}
	return nil
}

// lastStep reports whether id is the workflow's last declared step.
func (w *Workflow) lastStep(id string) bool {
	if w == nil {
		return false
	}
	i, ok := w.indexOf(id)
	return ok && i == len(w.Steps)-1
}

// stepVars answers the vars step id declares (nil for none, an unknown
// step or a nil workflow).
func (w *Workflow) stepVars(id string) map[string]string {
	if w == nil {
		return nil
	}
	if s := w.step(id); s != nil {
		return s.Vars
	}
	return nil
}

// LoadWorkflow reads a materialized bundle's workflow.md, parses its
// frontmatter and validates it.
func LoadWorkflow(bundleDir string) (*Workflow, error) {
	raw, err := os.ReadFile(filepath.Join(bundleDir, "workflow.md"))
	if err != nil {
		return nil, fmt.Errorf("driver: bundle workflow.md unreadable: %w", err)
	}
	front, _, ok := workflow.SplitFrontmatter(raw)
	if !ok {
		return nil, fmt.Errorf("driver: bundle workflow.md has no frontmatter block")
	}
	wf, err := parseWorkflow(front)
	if err != nil {
		return nil, err
	}
	if err := validateWorkflow(wf); err != nil {
		return nil, err
	}
	return wf, nil
}

// parseWorkflow parses one frontmatter block, filling the defaults:
// context_policy inherit, rewind bounds 4/2. Judging the filled values is
// validateWorkflow's job.
func parseWorkflow(front []byte) (*Workflow, error) {
	var doc extDoc
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return nil, fmt.Errorf("driver: bundle frontmatter unparsable: %w", err)
	}
	wf := &Workflow{
		MaxTotal:   defaultMaxRewindTotal,
		MaxPerStep: defaultMaxRewindPerStep,
	}
	if doc.Rewind != nil {
		if doc.Rewind.MaxTotal != nil {
			wf.MaxTotal = *doc.Rewind.MaxTotal
		}
		if doc.Rewind.MaxPerStep != nil {
			wf.MaxPerStep = *doc.Rewind.MaxPerStep
		}
	}
	for _, s := range doc.Steps {
		step := StepSpec{
			ID:                      s.ID,
			Gate:                    s.Gate,
			Handoff:                 s.Handoff,
			ContextPolicy:           s.ContextPolicy,
			RequiresHumanAnswer:     s.RequiresHumanAnswer.required,
			HumanAnswerIfAsked:      s.RequiresHumanAnswer.ifAsked,
			RequiresCommandEvidence: s.RequiresCommandEvidence,
			Vars:                    s.Vars,
		}
		if s.Budget != nil {
			step.WallMinutes = s.Budget.WallMinutes
		}
		if step.ContextPolicy == "" {
			step.ContextPolicy = ContextInherit // the declared default
		}
		if s.OnFail != nil {
			step.OnFail = &OnFail{Target: s.OnFail.Target}
		}
		wf.Steps = append(wf.Steps, step)
	}
	return wf, nil
}

// validateWorkflow refuses, at load, what the driver cannot honor:
// missing/duplicate step ids, context policies outside the closed set
// (every step carries one after parseWorkflow — the inherit default),
// gate steps that are not fresh (a gate must judge on its own declared
// inputs, never on an inherited persuasive context), and an on_fail that is
// not on a gate or whose target is not an earlier step of the workflow
// (otherwise the mistake would only surface at the gate's first FAIL).
func validateWorkflow(wf *Workflow) error {
	if wf == nil || len(wf.Steps) == 0 {
		return fmt.Errorf("driver: workflow has no steps")
	}
	seen := map[string]bool{}
	for i := range wf.Steps {
		s := &wf.Steps[i]
		if s.ID == "" {
			return fmt.Errorf("driver: workflow step %d without an id", i)
		}
		if seen[s.ID] {
			return fmt.Errorf("driver: duplicate step id %q", s.ID)
		}
		seen[s.ID] = true
		switch s.ContextPolicy {
		case ContextInherit, ContextFresh:
		default:
			return fmt.Errorf("driver: step %q has unknown context_policy %q (want %q or %q)",
				s.ID, s.ContextPolicy, ContextInherit, ContextFresh)
		}
		if s.Gate && s.ContextPolicy != ContextFresh {
			return fmt.Errorf("driver: gate step %q must run context_policy: %s (found %q)",
				s.ID, ContextFresh, s.ContextPolicy)
		}
		if s.OnFail != nil {
			if !s.Gate {
				return fmt.Errorf("driver: step %q declares on_fail but is not a gate", s.ID)
			}
			// seen holds this step and every step declared before it.
			if s.OnFail.Target == s.ID || !seen[s.OnFail.Target] {
				return fmt.Errorf("driver: gate %q on_fail target %q is not an earlier step of the workflow", s.ID, s.OnFail.Target)
			}
		}
	}
	return nil
}

// The loose frontmatter schema (unknown keys are ignored — the retired
// step `kind:` an old bundle snapshot may still carry included, so a
// same-daemon recovery of such a capsule loads it unchanged).
type extDoc struct {
	Rewind *extRewind `yaml:"rewind"`
	Steps  []extStep  `yaml:"steps"`
}

type extRewind struct {
	MaxTotal   *int `yaml:"max_total"`
	MaxPerStep *int `yaml:"max_per_step"`
}

type extStep struct {
	ID                      string            `yaml:"id"`
	Gate                    bool              `yaml:"gate"`
	Handoff                 bool              `yaml:"handoff"`
	RequiresHumanAnswer     humanGate         `yaml:"requires_human_answer"`
	RequiresCommandEvidence bool              `yaml:"requires_command_evidence"`
	ContextPolicy           string            `yaml:"context_policy"`
	OnFail                  *extOnFail        `yaml:"on_fail"`
	Budget                  *extBudget        `yaml:"budget"`
	Vars                    map[string]string `yaml:"vars"`
}

// humanGate is the requires_human_answer value: a bool (true = a human
// answer must precede completion) or the string "if_asked" (questions
// are human-only, zero questions pass with a recorded notification).
type humanGate struct {
	required bool
	ifAsked  bool
}

// HumanGateIfAsked is the requires_human_answer value that allows a
// zero-question completion (recorded and notified, never refused).
const HumanGateIfAsked = "if_asked"

func (h *humanGate) UnmarshalYAML(n *yaml.Node) error {
	var b bool
	if err := n.Decode(&b); err == nil {
		h.required = b
		return nil
	}
	var s string
	if err := n.Decode(&s); err == nil && s == HumanGateIfAsked {
		h.ifAsked = true
		return nil
	}
	return fmt.Errorf("requires_human_answer: want true, false or %q, got %q", HumanGateIfAsked, n.Value)
}

type extBudget struct {
	WallMinutes int `yaml:"wall_minutes"`
}

type extOnFail struct {
	Target string `yaml:"target"`
}
