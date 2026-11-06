package driver

// intent.go: the owner's move a hand-off citing the owner carries
// (`pf step handoff --status cancel|waive|rewind --per-owner <at>`,
// workflow contract 4; handoffShape routes it as an intent). The agent
// reads the owner's free-text message itself; the daemon checks only that
// the cited message is an owner message of the step's round and applies
// the move verbatim, judging no words:
//
//   - cancel: the durable cancel intent and the blocked park in one write,
//     the capsule kept; the daemon's blocked close ends the work item
//     canceled at the hub;
//   - retry (a rewind naming the step itself): this attempt is archived
//     (intent_retry) and the same step is dispatched again in a new
//     conversation, same round, no rewind, no charge;
//   - rewind (to an agent step before this one): the owner's send-back
//     path (ownerrewind.go), the owner class on the ledger;
//   - waive (a gate's FAIL; a `waivable: true` gate): no rewind, no
//     charge, the next step (waive.go, applyWaiveLocked).

import (
	"errors"
	"fmt"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// OutcomeIntent is the routed outcome of a cancel or rewind hand-off
// citing the owner (cancel, retry or rewind; no verdict is recorded).
const OutcomeIntent Outcome = "intent"

// The intent kinds.
const (
	IntentCancel = "cancel"
	IntentWaive  = "waive"
	IntentRetry  = "retry"
	IntentRewind = "rewind"
)

// StepIntent is the owner's move a hand-off citing the owner carries
// (handoffShape): what the daemon is to do, the owner message it rests on
// (ReplyAt: the --per-owner stamp, an owner message of this step's round
// in asks.jsonl), the rewind target (rewind only) and the hand-off's
// reason (the agent's reading of the message, in its words).
type StepIntent struct {
	Kind    string
	ReplyAt string
	To      string
	Reason  string
}

// CauseIntentRetry is the attempt cause of a dispatch that handed off a
// rewind to itself on the owner's word: the same step runs again, same
// round.
const CauseIntentRetry = "intent_retry"

// applyIntentLocked lands a cancel or rewind hand-off citing the owner
// (cancel, retry, rewind) on the current step in the result write; msg is
// the owner message the intent rests on (checked before the write).
func (d *Driver) applyIntentLocked(stepID string, st *capsule.Step, res *StepResult, msg *capsule.AskLine, info *appliedInfo) error {
	in := res.Intent
	if in == nil || msg == nil {
		return errors.New("driver: an intent write carries the intent and the owner message it rests on")
	}
	at := d.stamp()
	by := msg.By
	if by == "" {
		by = "owner"
	}
	info.extra = append(info.extra, appliedEvent{"intent", map[string]any{
		"spawn_n": d.spawnN, "step": stepID, "kind": in.Kind, "reply_at": in.ReplyAt, "to": in.To, "reason": clipRunes(in.Reason, 2000), "by": by,
	}})
	switch in.Kind {
	case IntentCancel:
		// the durable cancel intent and the park in one write; the
		// daemon's blocked close ends the work item canceled
		st.Dispatch = 0
		d.exec.CancelRequested = &capsule.CancelIntent{QuestionID: in.ReplyAt, Answer: msg.Text + "\n\n(the agent's reading: " + in.Reason + ")", AnsweredBy: by, At: at}
		d.exec.WorkflowStatus = StatusBlocked
		d.exec.BlockedReason = "cancel requested: step " + stepID + " handed off cancel on the owner's message " + in.ReplyAt + "; the work item ends in the hub's canceled status"
		info.extra = append(info.extra, appliedEvent{"blocked", map[string]any{
			"spawn_n": d.spawnN, "step": stepID, "reason": d.exec.BlockedReason, "report": false,
		}})
		return nil
	case IntentRetry:
		// this attempt is archived; the step stays in_progress in the same
		// round and the loop dispatches it again in a new conversation
		st.Dispatch = 0
		st.Attempts = append(st.Attempts, capsule.Attempt{
			Cause: CauseIntentRetry, AttemptSeq: nextAttemptSeq(st), StartedAt: st.StepStartedAt, ArchivedAt: at,
			Detail: "re-run on the owner's message " + in.ReplyAt + ": " + clipRunes(in.Reason, 600),
		})
		if st.Supervision == nil {
			st.Supervision = &capsule.StepSupervision{}
		}
		st.Supervision.NextDispatch = capsule.NextDispatchFresh
		return nil
	case IntentRewind:
		reason := "The owner's message (" + in.ReplyAt + ", " + by + "):\n\n" + msg.Text + "\n\nStep " + stepID + "'s reading, on which it sent the work item back:\n\n" + in.Reason
		st.Dispatch = 0
		return d.applyOwnerRewindLocked(TransitionRequest{OwnerRewind: &capsule.OwnerRewindRequest{To: in.To, Reason: reason, By: by}}, info)
	}
	return fmt.Errorf("driver: intent kind %q is not one a cancel or rewind hand-off carries", in.Kind)
}

// applyGateFailIntentLocked lands the waive a gate's hand-off carries
// instead of the gate's plain FAIL policy: msg is the owner message it
// rests on. It answers handled false when the hand-off carries no intent
// (the plain policy applies).
func (d *Driver) applyGateFailIntentLocked(gate string, st *capsule.Step, out map[string]any, res *StepResult, msg *capsule.AskLine, info *appliedInfo) (handled bool, err error) {
	in := res.Intent
	if in == nil {
		return false, nil
	}
	if msg == nil {
		return true, errors.New("driver: a waive write carries the owner message it rests on")
	}
	if in.Kind != IntentWaive {
		return true, fmt.Errorf("driver: intent kind %q is not one a gate's fail carries", in.Kind)
	}
	by := msg.By
	if by == "" {
		by = "owner"
	}
	info.extra = append(info.extra, appliedEvent{"intent", map[string]any{
		"spawn_n": d.spawnN, "step": gate, "kind": in.Kind, "reply_at": in.ReplyAt, "reason": clipRunes(in.Reason, 2000), "by": by,
	}})
	d.applyWaiveLocked(gate, st, out, in.ReplyAt, by, info)
	return true, nil
}
