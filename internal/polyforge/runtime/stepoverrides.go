package runtime

// stepoverrides.go: the step-overlay write path (polyforge#25) — one
// per-work-item actor turn per change, three postures:
//
//   - a RUNNING job: the overlay file in its capsule is rewritten
//     (read-modify-write), the change is tapped as an event, and the
//     capsule chain is committed and pushed at once (a failed push keeps
//     the local commit, exactly like commit-dirty-on-recover);
//   - a PAUSED record: the same write into the kept scene (a pause seals
//     nothing — stopOp's read-only seal is the terminal closes' — so the
//     scene stays writable); aihub refuses pushes for a non-running work
//     item, so the push is expected to fail, the local commit stands and
//     the resume's own publishes carry it;
//   - no job (the work item parked blocked): the change lands in the
//     daemon-local pending record (like the manual `--models` list), and
//     the next claim's scene preparation merges it into the fresh
//     capsule's overlay file — the sealed scene of the last close is
//     read-only and abandoned by the re-claim's fresh clone, so writing
//     it would lose the change.
//
// The step-status rule is re-checked under the actor (the one thing that
// can move between the route's validation and this turn): a step with a
// live dispatch is refused; a parked work item's frozen current step
// (in_progress, no process) accepts a model chain — the owner's
// change-before-wake window — while the skip switch stays for steps that
// never started.

import (
	"context"
	"errors"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// errOverrideNotImplemented is the stub's refusal, until u4's impl lands.
var errOverrideNotImplemented = errors.New("runtime: the step-override write is not implemented yet")

// pendingOverridesPath is the daemon-local pending record's path. Stub
// (polyforge#25 u4).
func (r *Runtime) pendingOverridesPath() string { return "" }

// savePendingOverrides records work item wi's pending overlay entries.
// Stub (polyforge#25 u4).
func (r *Runtime) savePendingOverrides(wi string, entries map[string]*capsule.StepOverride) error {
	return errOverrideNotImplemented
}

// mergePendingAspects merges pending entries over the capsule's overlay
// record, per aspect. Stub (polyforge#25 u4).
func mergePendingAspects(ov capsule.StepOverrides, pending map[string]*capsule.StepOverride) capsule.StepOverrides {
	return ov
}

// OverrideChange is one overlay write as the daemon's route validated it:
// exactly one action, the candidate chain already parsed and normalized.
type OverrideChange struct {
	Step        string
	Operator    string
	Models      []string // non-nil = set the chain
	ClearModels bool
	Skip        *bool // non-nil = set the switch (true/false)
}

// OverrideOutcome is what the actor turn did.
type OverrideOutcome struct {
	// Posture the write landed in: "running", "paused" or "blocked".
	Posture string
	// Changed is false when a clear found nothing (the idempotent
	// success: nothing written, no event).
	Changed bool
	// Published: the chain commit reached the server (see
	// protocol.OverrideWriteResult.Published for the posture-dependent
	// honesty rules).
	Published bool
	// Note explains an unpublished change (and anything else the caller
	// should surface).
	Note string
	// Entry is the step's overlay entry after the change (nil when
	// nothing changed).
	Entry *capsule.StepOverride
}

// ApplyStepOverride applies one overlay change to work item wi, one
// actor turn (the same per-work-item mailbox a claim or a resume runs
// on, so the write serializes with both). Stub (polyforge#25 u4).
func (r *Runtime) ApplyStepOverride(ctx context.Context, wi string, ch OverrideChange) (OverrideOutcome, error) {
	return OverrideOutcome{}, errOverrideNotImplemented
}

// PendingStepOverrides answers the daemon-local pending overlay entries
// of work item wi (nil when none): the changes recorded while the work
// item was held by no job, waiting for its next claim here. Stub
// (polyforge#25 u4).
func (r *Runtime) PendingStepOverrides(wi string) map[string]*capsule.StepOverride {
	return nil
}

// ManualModels answers the work item's frozen manual candidate list
// (`pf submit --models`), nil when none. Stub (polyforge#25 u4).
func (r *Runtime) ManualModels(wi string) []string {
	return nil
}
