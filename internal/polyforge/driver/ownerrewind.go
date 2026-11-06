package driver

// ownerrewind.go: the rewind the owner's word orders (workflow contract 4,
// design #483 §7). A step hands off `rewind --to <step> --per-owner <at>`
// citing the owner's message, and the work item goes back to that earlier
// step through the hand-off's result write (applyOwnerRewindLocked): the
// steps from the target through the current one are archived and reset
// (the target with a rewind target cause, the others superseded) and the
// rewind ledger records it with who, when, from where, to where and why.
// Its class is the owner class: it never counts toward the lane's
// max_rewinds — which bounds the automatic gate loops — and the owner's
// rewinds are counted on their own (OwnerRewindCount).
//
// The owner sends a work item back by telling its step, never by a command
// of their own: `pf rewind` and its doors (the daemon route, the blocked
// door through aihub's attrs.owner_rewind) were removed in workflow
// contract 4 C3.

import (
	"fmt"
	"strings"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// EvOwnerRewind is the owner's rewind's event: who, when, from, to, the
// reason and the archived steps.
const EvOwnerRewind = "owner_rewind"

// OwnerRewindTargets answers the steps a rewind the owner orders may name
// now: every agent step before the current one, in workflow order. None
// for a terminal workflow, one that has not started a step, or a current
// step the workflow does not declare.
func OwnerRewindTargets(wf *Workflow, ex *capsule.Execution) []string {
	if wf == nil || ex == nil || isTerminalStatus(ex.WorkflowStatus) || ex.CurrentStep == "" {
		return nil
	}
	cur, ok := wf.indexOf(ex.CurrentStep)
	if !ok {
		return nil
	}
	var out []string
	for _, s := range wf.Steps[:cur] {
		out = append(out, s.ID)
	}
	return out
}

// OwnerRewindCount answers how many of the ledger's rewinds were the
// owner's (charged to no budget): the count shown beside the agents'.
func OwnerRewindCount(rewinds []capsule.RewindRecord) int64 {
	var n int64
	for _, r := range rewinds {
		if r.Class == capsule.RewindOwner {
			n++
		}
	}
	return n
}

// MaxRewinds answers the bound the automatic rewinds count toward: the
// lane's max_rewinds (0 for a nil workflow).
func MaxRewinds(wf *Workflow) int {
	if wf == nil {
		return 0
	}
	return wf.MaxRewinds
}

// AgentRewindCount answers how many rewinds count toward max_rewinds: the
// automatic ones, the budget the owner's rewinds never spend.
func AgentRewindCount(rewinds []capsule.RewindRecord) int64 {
	return countTowardTotal(rewinds)
}

// checkOwnerRewindLocked answers nil when req may rewind the work item now:
// the workflow has not ended and the target is an agent step before the
// current one (the daemon's `pf step handoff` refuses any other target up
// front; this is the write's own guard).
func (d *Driver) checkOwnerRewindLocked(req capsule.OwnerRewindRequest) error {
	if isTerminalStatus(d.exec.WorkflowStatus) {
		return fmt.Errorf("driver: the workflow ended (%s); a finished work item is not sent back", d.exec.WorkflowStatus)
	}
	targets := OwnerRewindTargets(d.workflow, d.exec)
	for _, t := range targets {
		if t == req.To {
			return nil
		}
	}
	why := fmt.Sprintf("step %q is not an agent step before the current step %q", req.To, d.exec.CurrentStep)
	switch {
	case d.exec.CurrentStep == "":
		why = "no step has started yet, so there is nothing to send back"
	case d.workflow.step(req.To) == nil:
		why = fmt.Sprintf("step %q is not in this work item's workflow", req.To)
	}
	if len(targets) > 0 {
		why += "; the steps it can be sent back to now: " + strings.Join(targets, ", ")
	}
	return fmt.Errorf("driver: %s", why)
}

// applyOwnerRewindLocked lands the owner's rewind: [target, current]
// archived and reset (the owner class in the ledger, with by/at/reason; the
// step reads the reason in the 回退 section of artifacts/handoff.md), every
// pending question of the range withdrawn, the workflow running again.
func (d *Driver) applyOwnerRewindLocked(req TransitionRequest, info *appliedInfo) error {
	rw := req.OwnerRewind
	if rw == nil {
		return fmt.Errorf("driver: an owner rewind write carries no request")
	}
	if err := d.checkOwnerRewindLocked(*rw); err != nil {
		return err
	}
	from := d.exec.CurrentStep
	tIdx, _ := d.workflow.indexOf(rw.To)
	cIdx, _ := d.workflow.indexOf(from)
	wasStatus := d.exec.WorkflowStatus
	at := d.stamp()
	d.applyRewindRangeLocked(tIdx, cIdx+1, rw.To, from, CauseGateFailRewind, capsule.RewindOwner, info)
	rec := &d.exec.RewindStats.Rewinds[len(d.exec.RewindStats.Rewinds)-1]
	rec.By, rec.At, rec.Reason = rw.By, at, rw.Reason
	// the owner's move supersedes a park and — on a blocked work item claimed
	// again (a live one refused above) — an owner's cancel that never
	// reached the hub (the unblock's rule)
	d.exec.WorkflowStatus = StatusRunning
	d.exec.BlockedReason = ""
	d.exec.CancelRequested = nil
	info.extra = append(info.extra, appliedEvent{EvOwnerRewind, map[string]any{
		"spawn_n": d.spawnN, "by": rw.By, "at": at, "from": from, "to": rw.To, "reason": clipRunes(rw.Reason, 2000),
		"archived": info.archived, "was": wasStatus,
		"owner_rewinds": OwnerRewindCount(d.exec.RewindStats.Rewinds),
		"agent_rewinds": AgentRewindCount(d.exec.RewindStats.Rewinds),
	}})
	return nil
}
