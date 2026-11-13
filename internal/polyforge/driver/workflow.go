package driver

// workflow.go: the driver's view of a lane. LoadWorkflow reads the lane
// through the one lane loader (workflow.Load) — the strict parse every
// reader of a lane shares, which refuses any lane the contract does not
// accept with every WFC-* defect at once — and maps the loaded lane onto
// the execution graph the driver and its spawn adapter run.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// Workflow is the execution graph: the step order IS the declaration
// order (rule V3), plus the lane's rewind budget.
type Workflow struct {
	// Name is the lane's name (its directory's).
	Name  string
	Steps []StepSpec
	// MaxRewinds is the lane's `max_rewinds: <n>`: ONE
	// total budget across every gate→target edge — every automatic rewind
	// counts toward it (an owner-routed one counts
	// toward nothing). 0 is a valid budget: every gate FAIL asks the owner.
	MaxRewinds int
}

// StepSpec is one workflow step: identity and order, gate policy and the
// artifact surface the spawn adapter assembles prompts from. Candidate
// lists are not read here: each dispatch composes them (Config.Compose).
type StepSpec struct {
	ID     string
	Gate   bool
	OnFail *OnFail
	// The step's wall-budget estimate (budget.wall_minutes, T's base).
	WallMinutes int
	// ToolMinutes is the step's own supervision tool window Y (0 = the
	// daemon's global window): a declared one replaces the global default
	// for the step's dispatches, rides every step process as
	// PF_TOOL_WINDOW_MS, and sizes a claude step's Bash default timeout to
	// the max. It is the contract-2 `budget.tool_minutes` key (workflow
	// contract v2 O1).
	ToolMinutes int
	// Waivable mirrors `waivable` (contract 2; default false): the gate
	// takes a hand-off `waive --per-owner`.
	Waivable bool
	// Skippable mirrors `skippable` (default false): a
	// project may switch the step off. Skipped is set by WithSkipped: the
	// step is skippable and the work item's skipped-step set names it, so
	// the driver never dispatches it.
	Skippable bool
	Skipped   bool
	// Skill mirrors `skill` ("" = none): the lane snapshot's skill file
	// rendered into the prompt as the `# Skill` section.
	Skill string
	// Resources mirrors `resources` (nil = none): what the daemon takes
	// for each dispatch of the step (resources.go).
	Resources *workflow.Resources
	// Vars fills the {{name}} placeholders of the step's definition file:
	// the lane-specific facts a shared workflows/_shared/steps/<id>.md
	// leaves to each lane.
	Vars map[string]string
	// Hub mirrors `hub.verbs`, HubAttrs `hub.attrs` and WrapAttrs
	// `wrap_attrs` (workflow contracts 4 and 5): the `pf step hub` verbs
	// the step may call, the attrs keys it may write, and the wrap attrs
	// its round registers before it may hand off done.
	Hub       []string
	HubAttrs  []string
	WrapAttrs []string
}

// HubAllowed answers the `pf step hub` verbs step id may call: its
// `hub.verbs`, wrap-attrs when it declares `wrap_attrs`, attrs when its
// `hub.attrs` names a key (nil for an unknown step or a nil workflow) —
// the daemon's whitelist of the step's calls.
func (w *Workflow) HubAllowed(id string) []string {
	if w == nil {
		return nil
	}
	s := w.step(id)
	if s == nil {
		return nil
	}
	return (&workflow.Step{Hub: s.Hub, HubAttrs: s.HubAttrs, WrapAttrs: s.WrapAttrs}).HubAllowed()
}

// HubAttrs answers the attrs keys step id may write with `pf step hub
// attrs` (nil for none, an unknown step or a nil workflow) — the daemon's
// whitelist of the step's attrs writes.
func (w *Workflow) HubAttrs(id string) []string {
	if w == nil {
		return nil
	}
	if s := w.step(id); s != nil {
		return s.HubAttrs
	}
	return nil
}

// WrapAttrs answers the wrap attrs step id's round must register before
// it hands off done (nil for none, an unknown step or a nil workflow).
func (w *Workflow) WrapAttrs(id string) []string {
	if w == nil {
		return nil
	}
	if s := w.step(id); s != nil {
		return s.WrapAttrs
	}
	return nil
}

// Waivable reports whether the owner may waive a FAIL of step id
// (contract 2): a gate declaring `waivable: true` (default false). The
// daemon's waive route asks it of the parked capsule's lane.
func (w *Workflow) Waivable(id string) bool {
	if w == nil {
		return false
	}
	s := w.step(id)
	return s != nil && s.Gate && s.Waivable
}

// IsGate reports whether step id declares gate: true.
func (w *Workflow) IsGate(id string) bool {
	s := w.step(id)
	return s != nil && s.Gate
}

// OnFail is the gate FAIL policy: the rewind target the gate's FAIL
// verdict rewinds to.
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

// lastAgentStep reports whether id is the workflow's last step not
// skipped (StepSpec.Skipped): the last step an agent runs — the one that
// owes the work item's wrap summary, and the delivery a waiver cannot
// skip.
func (w *Workflow) lastAgentStep(id string) bool {
	if w == nil {
		return false
	}
	for i := len(w.Steps) - 1; i >= 0; i-- {
		if !w.Steps[i].Skipped {
			return w.Steps[i].ID == id
		}
	}
	return false
}

// skipped reports whether the driver skips step id (StepSpec.Skipped).
func (w *Workflow) skipped(id string) bool {
	s := w.step(id)
	return s != nil && s.Skipped
}

// ReadSkippedSteps reads the work item's skipped-step set the daemon
// fixed in the capsule at the first claim (capsule.SkippedStepsJSON); a
// capsule without one (or with an unreadable one) skips nothing.
func ReadSkippedSteps(capsuleDir string) []string {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, filepath.FromSlash(capsule.SkippedStepsJSON)))
	if err != nil {
		return nil
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		return nil
	}
	return ids
}

// WithSkipped answers the workflow with the steps of ids that it declares
// skippable marked Skipped; a listed step the lane does
// not declare skippable runs as usual. The wrap attrs of the skipped steps
// at the lane's tail (after the last step that runs) move to that last
// running step: its round registers them before it hands off done, and it
// may call wrap-attrs (HubAllowed). The receiver is never changed: a copy
// is answered when a step is marked, else the receiver itself.
func (w *Workflow) WithSkipped(ids []string) *Workflow {
	if w == nil || len(ids) == 0 {
		return w
	}
	listed := map[string]bool{}
	for _, id := range ids {
		listed[id] = true
	}
	var out *Workflow
	for i, s := range w.Steps {
		if !s.Skippable || s.Skipped || !listed[s.ID] {
			continue
		}
		if out == nil {
			cp := *w
			cp.Steps = append([]StepSpec(nil), w.Steps...)
			out = &cp
		}
		out.Steps[i].Skipped = true
	}
	if out == nil {
		return w
	}
	last := len(out.Steps) - 1
	for last >= 0 && out.Steps[last].Skipped {
		last--
	}
	if last < 0 {
		return out
	}
	moved := append([]string(nil), out.Steps[last].WrapAttrs...)
	has := map[string]bool{}
	for _, name := range moved {
		has[name] = true
	}
	for _, s := range out.Steps[last+1:] {
		for _, name := range s.WrapAttrs {
			if !has[name] {
				has[name] = true
				moved = append(moved, name)
			}
		}
	}
	if len(moved) > len(out.Steps[last].WrapAttrs) {
		out.Steps[last].WrapAttrs = moved
	}
	return out
}

// LoadWorkflow reads the lane at bundleDir through the one lane loader
// and answers its execution graph; a lane with any contract defect is
// refused with every defect (*workflow.LoadError).
func LoadWorkflow(bundleDir string) (*Workflow, error) {
	lane, err := workflow.LoadStrict(bundleDir)
	if err != nil {
		return nil, err
	}
	return workflowOf(lane), nil
}

// LoadWorkflowGraph loads a lane's step graph — the step entries of its
// workflow.yaml (gate, on_fail, waivable, wrap_attrs, hub, …) — even when a
// step definition, a skill or a fragment it renders is defective: those
// are the driver's to meet when it dispatches the step that needs them.
// It fails only when workflow.yaml itself carries a defect (or cannot be
// read). The daemon's `pf step` checks read it (a hand-off's status and
// wrap attrs, the hub whitelist): a broken definition of a later step
// must not refuse this step's hand-off.
func LoadWorkflowGraph(bundleDir string) (*Workflow, error) {
	lane, errs := workflow.Load(bundleDir)
	front := filepath.Clean(filepath.Join(bundleDir, workflow.WorkflowFile))
	var own []workflow.ContractError
	for _, e := range errs {
		if e.File == "" || filepath.Clean(e.File) == front {
			own = append(own, e)
		}
	}
	if err := workflow.AsLoadError(bundleDir, own); err != nil {
		return nil, err
	}
	if lane == nil || len(lane.Steps) == 0 {
		return nil, fmt.Errorf("driver: lane %s has no step graph", bundleDir)
	}
	return workflowOf(lane), nil
}

// workflowOf maps a loaded lane onto the execution graph.
func workflowOf(lane *workflow.Lane) *Workflow {
	wf := &Workflow{Name: lane.Name, MaxRewinds: lane.MaxRewinds}
	for _, s := range lane.Steps {
		step := StepSpec{
			ID:          s.ID,
			Gate:        s.Gate,
			WallMinutes: s.WallMinutes,
			ToolMinutes: s.ToolMinutes,
			Waivable:    s.Waivable,
			Skippable:   s.Skippable,
			Skill:       s.Skill,
			Resources:   s.Resources,
			Vars:        s.Vars,
			Hub:         s.Hub,
			HubAttrs:    s.HubAttrs,
			WrapAttrs:   s.WrapAttrs,
		}
		if s.OnFailTarget != "" {
			step.OnFail = &OnFail{Target: s.OnFailTarget}
		}
		wf.Steps = append(wf.Steps, step)
	}
	return wf
}
