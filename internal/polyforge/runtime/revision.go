package runtime

// revision.go: the runtime's owner revision door. A live
// driver revises through its own single writer; a retained PAUSED record
// has no Run loop, so a record-only driver applies the same transition and
// the runtime commits the corrected execution.json to the capsule chain.
// Both shapes persist the correction in execution.json itself, so a later
// re-claim's fresh clone carries it — no history rewrite, no override file.
//
// Who can reach it: the daemon route sits behind refuseStepOrigin, which
// refuses a step (or task) process tree DAEMON-403 before any handler runs.
// There is no agent-facing path to this file or to the driver handle.

import (
	"context"
	"fmt"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// bindingReviser is the live driver handle's revision face. The correction
// shape is capsule.BindingCorrection, shared with the driver and the paused
// applier: the runtime package cannot import driver because the driver's
// internal tests import runtime (the test-only import cycle), so the daemon
// binds the record-only applier through Config.ReviseRecord.
type bindingReviser interface {
	ReviseBinding(stepID string, corrections []capsule.BindingCorrection, by, reason string) error
}

// ReviseBinding applies one owner revision batch of a done step's binding
// commands. It is a message of the work item's actor, like answer/resume,
// so it serializes with every other operation on that work item. The batch
// is one transition: a result whose bindings all carry the same malformed
// quote is corrected in one pass.
func (r *Runtime) ReviseBinding(ctx context.Context, wi, stepID string, corrections []capsule.BindingCorrection, by, reason string) error {
	id := r.CanonicalID(wi)
	return r.call(ctx, id, func() error {
		j := r.jobOf(id)
		if j == nil {
			return &api.Error{Code: api.ErrDaemonNotFound, Detail: "no running or paused work item " + id + " held by this daemon; `pf resume` it first"}
		}
		state, spawn := j.snapshot()
		j.mu.Lock()
		agent := j.agent
		capsuleDir, bundleName, fingerprint, wiID := j.capsuleDir, j.bundleName, j.sessionFingerprint, j.wi
		gitOpts := j.git
		j.mu.Unlock()

		switch state {
		case StateRunning:
			// The live driver owns execution.json: never write beside it.
			h, ok := agent.(bindingReviser)
			if !ok {
				return conflict("work item %s is running but its current engine exposes no live driver revision face; retry once the next derivation starts", id)
			}
			return h.ReviseBinding(stepID, corrections, by, reason)
		case StatePaused:
			if j.closeParked() {
				return j.parkedError(id)
			}
			rec := j.record()
			if closePending(rec) {
				return conflict("a %s close of the paused record is pending (its SessionEnd has not reached aihub); %s — nothing was revised", rec.StopReason, j.closeRetryNote())
			}
			// The session verify is the fence reconcileRecord requires before
			// any push under a retained record's session. A session the hub no
			// longer holds settles the record instead of appending to a chain
			// the session can no longer write.
			v, verr := r.hub.SessionVerify(rec.WorkItemID, rec.SessionID)
			if verr != nil {
				return &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: fmt.Sprintf("%s: session verify failed before the revision (nothing was revised; retry): %v", id, verr)}
			}
			if !v.Matches {
				if err := r.notOurs(j, v); err != nil {
					return err
				}
				return conflict("the session no longer holds %s (current holder %q); the local record was released and nothing was revised", rec.WorkItemID, v.CurrentFingerprint)
			}
			if r.cfg.ReviseRecord == nil {
				return conflict("no record revision applier is bound; the paused capsule cannot be revised on this daemon")
			}
			if err := r.cfg.ReviseRecord(capsuleDir, bundleName, fingerprint, wiID, int64(spawn), stepID, corrections, by, reason); err != nil {
				return err
			}
			// The correction must reach the canonical chain now, not only
			// this machine's worktree: the next claim may be on another
			// machine and fresh-clones from it. This is the same
			// commit+push the pause close and recovery use.
			if _, _, _, err := gitops.CommitPush(ctx, gitOpts, "revise: binding commands of step "+stepID+" (owner)"); err != nil {
				return conflict("the revision is written to the local capsule, but its chain commit/push failed: %v — retry the revision (or resume this record in place) once the push fault clears", err)
			}
			return nil
		default:
			return conflict("work item %s is %s; only a running driver or a retained paused record accepts an owner revision", id, state)
		}
	})
}
