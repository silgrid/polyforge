package driver

// revision.go: the owner's revision face for an already-accepted step
// output. The reported defect: an e2e-author binding
// command with an unpaired quote was accepted, became the canonical
// record, and verify-run could only exit 2. The owner's file-level fix
// never survived, because transition is the single writer and a
// re-claim fresh-clones the chain.
//
// The shortest legitimate path is one more transition: the correction
// enters through the same single writer as every other execution-state
// mutation, so it is persisted atomically and the normal observe publish
// appends it to the chain. No history rewrite, no override file, no
// second source of truth. The revision is deliberately narrow — one
// binding command of one done step, everything else unchanged — because
// the work item's non-goal says this is not a general step-output editor.
//
// Who can reach it: only a human session. The daemon route carries the
// existing step-origin guard (a step process tree is refused DAEMON-403),
// and the CLI verb is the operator's caller. A step agent cannot reach
// the transition: it never holds the driver, and its only daemon writes
// are /step/memories and /step/result.

import (
	"context"
	"errors"
	"strings"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// EvBindingRevised is the durable event kind of one owner revision of a
// done step's binding command. It carries the before/after command so the
// correction is visible next to the chain commit, not only inside the
// execution record.
const EvBindingRevised = "binding_revised"

// BindingRevisionRequest is one owner revision request: the bindings named
// in Corrections each get their Command, and nothing else changes. One
// request applies every correction in one transition, so a result whose
// bindings all carry the same malformed quote is corrected in one pass.
type BindingRevisionRequest struct {
	Corrections []capsule.BindingCorrection
	By          string
	Reason      string
}

// ReviseBinding applies an owner revision through the single write entry
// point. It answers the same fence and validation refusals a step result
// would: only a done step of a non-terminal workflow accepts one, and every
// corrected command must parse as a shell command exactly like the
// delivery gate demands. The complete revised output is validated as a
// whole, so a batch that still leaves any binding malformed is refused.
func (d *Driver) ReviseBinding(stepID string, rev BindingRevisionRequest) error {
	// Prepare and validate OUTSIDE the transition lock: the shell parse
	// starts a bash process (I/O), and the lock must never be held across
	// I/O. The transition then re-checks that the bindings it prepared are
	// still the ones on the record before replacing them.
	d.mu.Lock()
	if isTerminalStatus(d.exec.WorkflowStatus) {
		d.mu.Unlock()
		return errors.New("driver: workflow is terminal; revisions are frozen")
	}
	if _, ok := d.workflow.indexOf(stepID); !ok {
		d.mu.Unlock()
		return errors.New("driver: step " + stepID + " is not in the workflow")
	}
	st := d.exec.Steps[stepID]
	var output map[string]any
	var status string
	if st != nil {
		status = st.Status
		output = cloneBindingOutput(st.Output)
	}
	d.mu.Unlock()

	if st == nil || status != capsule.StepDone {
		return errors.New("driver: only a done step accepts an owner revision")
	}
	out, before, err := revisedBindingOutput(output, rev)
	if err != nil {
		return err
	}
	return d.transition(TransitionRequest{
		Kind:            TransStepRevision,
		StepID:          stepID,
		BindingRevision: &rev,
		RevisionOutput:  out,
		RevisionBefore:  before,
		SpawnN:          d.currentSpawnN(),
	})
}

// cloneBindingOutput copies the output map, its bindings slice and every
// binding object, so the shell validation and the transition can run
// outside the driver lock without racing a concurrent revision.
func cloneBindingOutput(output map[string]any) map[string]any {
	if output == nil {
		return nil
	}
	out := make(map[string]any, len(output))
	for k, v := range output {
		out[k] = v
	}
	list, ok := output["bindings"].([]any)
	if !ok {
		return out
	}
	listCopy := make([]any, len(list))
	for i, item := range list {
		if b, ok := item.(map[string]any); ok {
			bc := make(map[string]any, len(b))
			for k, v := range b {
				bc[k] = v
			}
			listCopy[i] = bc
		} else {
			listCopy[i] = item
		}
	}
	out["bindings"] = listCopy
	return out
}

// revisedBindingOutput validates a batch of corrections and answers the
// complete revised output plus the commands they replace (aligned with
// rev.Corrections). Every corrected command is parsed by bash -n (never
// executed), then all checks a delivered e2e-author result meets are
// applied to the complete output. Holding corrections in one batch is
// what lets a record whose bindings all carry the same malformed quote be
// corrected in one pass: validating the whole output after each single
// index would refuse every step while its siblings are still malformed.
func revisedBindingOutput(output map[string]any, rev BindingRevisionRequest) (map[string]any, []string, error) {
	if len(rev.Corrections) == 0 {
		return nil, nil, errors.New("driver: a binding revision needs at least one correction")
	}
	list, ok := output["bindings"].([]any)
	if !ok {
		return nil, nil, errors.New("driver: the step output has no bindings list to revise")
	}
	before := make([]string, len(rev.Corrections))
	seen := make(map[int]bool, len(rev.Corrections))
	for i, corr := range rev.Corrections {
		if seen[corr.Index] {
			return nil, nil, errors.New("driver: a binding revision names the same index twice")
		}
		seen[corr.Index] = true
		if corr.Index < 0 || corr.Index >= len(list) {
			return nil, nil, errors.New("driver: binding revision index out of range")
		}
		b, ok := list[corr.Index].(map[string]any)
		if !ok {
			return nil, nil, errors.New("driver: the named binding is not an object")
		}
		before[i], _ = b["command"].(string)
		if strings.TrimSpace(corr.Command) == "" {
			return nil, nil, errors.New("driver: a binding revision needs a non-empty command")
		}
		if err := checkShellSyntax(corr.Command); err != nil {
			return nil, nil, errors.New("driver: the revised binding command does not parse as a shell command: " + err.Error())
		}
		b["command"] = corr.Command
	}
	if err := checkBindings(output); err != nil {
		return nil, nil, errors.New("driver: the revised output is still invalid: " + err.Error())
	}
	return output, before, nil
}

// OpenRecord builds a Driver over an existing capsule for a record-only
// owner revision. There is no Run loop and no step process: the caller
// supplies no Spawner, and a stray Run fails loudly instead of spawning.
// The daemon uses it for a retained PAUSED record, where no live driver
// owns the capsule's execution.json; a RUNNING record revises through its
// live handle instead.
func OpenRecord(cfg Config) (*Driver, error) {
	if cfg.Spawner == nil {
		cfg.Spawner = recordOnlySpawner{}
	}
	return New(cfg)
}

// recordOnlySpawner is OpenRecord's refusal: a record-only driver must
// never derive a step process.
type recordOnlySpawner struct{}

func (recordOnlySpawner) SpawnStep(context.Context, StepSpawn) (StepRun, error) {
	return nil, errors.New("driver: a record-only driver cannot spawn a step process")
}

// applyBindingRevisionLocked lands one owner revision batch of a done
// e2e-author result. It performs no I/O and no shell
// parse: ReviseBinding prepared and validated the complete revised output
// outside the lock, and this write re-checks that every binding it
// prepared is still the one on the record. One ledger entry per corrected
// index and the output update land in the same atomic write, so a crash
// can never show a corrected command without its audit record (or the
// reverse).
func (d *Driver) applyBindingRevisionLocked(req TransitionRequest) error {
	rev := req.BindingRevision
	if isTerminalStatus(d.exec.WorkflowStatus) {
		return errors.New("driver: workflow is terminal; revisions are frozen")
	}
	if _, ok := d.workflow.indexOf(req.StepID); !ok {
		return errors.New("driver: step " + req.StepID + " is not in the workflow")
	}
	st := d.exec.Steps[req.StepID]
	if st == nil || st.Status != capsule.StepDone {
		return errors.New("driver: only a done step accepts an owner revision")
	}
	if rev == nil || req.RevisionOutput == nil || len(rev.Corrections) == 0 || len(req.RevisionBefore) != len(rev.Corrections) {
		return errors.New("driver: step_revision without a validated revision")
	}
	list, ok := st.Output["bindings"].([]any)
	if !ok {
		return errors.New("driver: the step output no longer has bindings to revise")
	}
	seen := make(map[int]bool, len(rev.Corrections))
	for i, corr := range rev.Corrections {
		if seen[corr.Index] {
			return errors.New("driver: a binding revision names the same index twice")
		}
		seen[corr.Index] = true
		if corr.Index < 0 || corr.Index >= len(list) {
			return errors.New("driver: the step output no longer has that binding")
		}
		b, ok := list[corr.Index].(map[string]any)
		if !ok {
			return errors.New("driver: the named binding is not an object")
		}
		if old, _ := b["command"].(string); old != req.RevisionBefore[i] {
			return errors.New("driver: the binding changed while the revision was being prepared; retry it")
		}
	}

	by := strings.TrimSpace(rev.By)
	if by == "" {
		by = "owner"
	}
	st.Output = req.RevisionOutput
	at := d.stamp()
	for i, corr := range rev.Corrections {
		st.BindingRevisions = append(st.BindingRevisions, capsule.BindingRevision{
			Index: corr.Index, Before: req.RevisionBefore[i], After: corr.Command, By: by, Reason: rev.Reason, At: at,
		})
	}
	return nil
}
