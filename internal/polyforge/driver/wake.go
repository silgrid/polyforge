package driver

// wake.go: the owner's message to a parked work item (workflow contract 4,
// design #483 §7 "叫醒谁"). A message no longer bounces off a
// parked work item: it wakes the step the park belongs to, and the daemon
// reads none of its words. WakeOf names, from the capsule's own records,
// which step that is and how it continues:
//
//   - the step handed off `blocked` (its own park): the step's session
//     resumes with the message alone — the conversation already holds its
//     prompt and its reason for stopping (WakeStepBlocked);
//   - the hub writes of a `done` hand-off failed: the resume replays them,
//     then the workflow continues by their outcome (WakeWriteFailed);
//   - any other park — a supervision block (timeouts, repeated exits), a
//     cancel that never landed, a contract 3 park: that step is dispatched
//     again in place, the message riding its prompt (WakeStep);
//   - the agent cannot start — no usable model (NoUsableModelBlockPrefix),
//     a candidate or attr composition that failed: no message changes
//     that, so the park stays and the refusal says why and names `pf
//     resume` / `pf cancel` (WakeCannotStart);
//   - the capsule's lane snapshot targets another workflow contract — a
//     `contract` park, or a work item of the previous contract parked
//     (for any reason) before the upgrade, whose claim would only park it
//     `contract`: the refusal names `pf cancel` alone (cancel and file it
//     again; a resume parks it again) (WakeCannotStart, ContractBlocked of
//     its Why).
//
// A paused work item (the operator's pause) wakes its current step too;
// the daemon tells it apart by its job state, not by execution.json.
//
// The wake itself is the owner's door: `pf answer` opens the same door
// `pf resume` opens with the caller's own key, then the daemon records the
// message (asks.jsonl, the record first) and claims the work item; the
// claim's ResumeCurrent re-dispatches the step and its dispatch carries the
// undelivered message (relay.go). The daemon never unblocks a work item at
// aihub on its own.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// WakeKind names how an owner's message wakes a parked work item.
type WakeKind string

const (
	// WakeNone: the work item is not parked (the live relay takes the
	// message, or there is nothing to talk to).
	WakeNone WakeKind = ""
	// WakeStepBlocked: the step handed off `blocked`; its session resumes
	// with the message alone.
	WakeStepBlocked WakeKind = "step_blocked"
	// WakeStep: any other park of a step (a supervision block, ...): that
	// step is dispatched again with the message.
	WakeStep WakeKind = "step"
	// WakeWriteFailed: the hub writes of a `done` hand-off failed; the
	// resume replays them, then continues.
	WakeWriteFailed WakeKind = "write_failed"
	// WakePaused: the operator paused the work item; its current step
	// continues in place with the message.
	WakePaused WakeKind = "paused"
	// WakeCannotStart: the step's agent cannot start; the message cannot
	// wake it.
	WakeCannotStart WakeKind = "cannot_start"
)

// Wake is WakeOf's answer: the kind, the step the message is recorded
// for and the dispatch it addresses (the step's newest), and — for
// WakeCannotStart — why.
type Wake struct {
	Kind  WakeKind
	Step  string
	Spawn int64
	Why   string
}

// compositionFailedMarker is the phrase composeDispatch's block reason
// carries for a candidate or attr composition that failed at dispatch
// time (TestWakeOfCompositionFailure pins the two together).
const compositionFailedMarker = "composition failed at dispatch time"

// WakeOf classifies the park of a blocked execution read from capsuleDir:
// WakeNone when ex is not blocked. It reads execution.json's blocked
// reason, the capsule's lane snapshot (its contract version), the call
// ledger and the spawn journal only; an unreadable ledger or journal
// degrades to WakeStep (the step is dispatched again with the message —
// never a wrong refusal).
func WakeOf(capsuleDir string, ex *capsule.Execution) Wake {
	if ex == nil || ex.WorkflowStatus != StatusBlocked {
		return Wake{}
	}
	w := Wake{Kind: WakeStep, Step: ex.CurrentStep}
	w.Spawn = ParkedDispatch(capsuleDir, ex)
	reason := ex.BlockedReason
	switch {
	case ContractBlocked(reason):
		w.Kind, w.Why = WakeCannotStart, reason
		return w
	case strings.HasPrefix(reason, NoUsableModelBlockPrefix), strings.Contains(reason, compositionFailedMarker):
		w.Kind, w.Why = WakeCannotStart, reason
		return w
	}
	// a park of the previous contract (made before the upgrade, whatever
	// its reason): the claim the wake ends in would only park it
	// `contract` (blockOnContract), the message never reaching a step
	if why := capsuleContractMismatch(capsuleDir); why != "" {
		w.Kind, w.Why = WakeCannotStart, why
		return w
	}
	lines, err := stepcall.Read(capsuleDir)
	if err != nil || w.Step == "" {
		return w
	}
	if last := stepcall.LastHandoff(lines, w.Step); last != nil && last.Handoff.Status == stepcall.StatusDone &&
		stepcall.WritesUnfinished(lines, w.Step, last.Round) {
		w.Kind, w.Spawn = WakeWriteFailed, last.Spawn
		return w
	}
	if w.Spawn > 0 && stepcall.EndedWith(lines, w.Step, w.Spawn) == stepcall.StatusBlocked {
		w.Kind = WakeStepBlocked
	}
	return w
}

// CannotStartRefusal is the owner-facing refusal of a message to a park
// whose agent cannot start: why, and the moves left — `pf resume` or `pf
// cancel`, but `pf cancel` alone for a lane snapshot of another contract
// (ContractBlocked of the Why: a resume parks it again).
func CannotStartRefusal(wi string, w Wake) string {
	if ContractBlocked(w.Why) {
		return fmt.Sprintf("%s is parked and its agent cannot start, so a message cannot wake it: %s — `pf cancel %s` and file it again on an installed lane: a resume parks it again (nothing was recorded)",
			wi, w.Why, wi)
	}
	return fmt.Sprintf("%s is parked and its agent cannot start, so a message cannot wake it: %s — fix the cause, then `pf resume %s` to continue, or `pf cancel %s` to abandon it (nothing was recorded)",
		wi, w.Why, wi, wi)
}

// capsuleContractMismatch answers the contract park's reason when the
// lane snapshot of the capsule at capsuleDir (capsule.json's bundle name
// under its workflows/) targets another workflow contract than this
// daemon implements ("" when it does not, or the snapshot cannot be
// found: never a wrong refusal).
func capsuleContractMismatch(capsuleDir string) string {
	if capsuleDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(capsuleDir, capsule.CapsuleJSON))
	if err != nil {
		return ""
	}
	var idn capsule.Identity
	if json.Unmarshal(raw, &idn) != nil || idn.BundleName == "" {
		return ""
	}
	return snapshotContractMismatch(filepath.Join(capsuleDir, capsule.DirWorkflows, idn.BundleName))
}

// ParkedDispatch answers the dispatch a message to the parked step
// addresses: the step's awaited dispatch when execution.json still names
// one, else the step's newest dispatch on the spawn journal (0 when none
// ran or the journal is unreadable).
func ParkedDispatch(capsuleDir string, ex *capsule.Execution) int64 {
	if ex == nil || ex.CurrentStep == "" {
		return 0
	}
	if st := ex.Steps[ex.CurrentStep]; st != nil && st.Dispatch > 0 {
		return st.Dispatch
	}
	return lastDispatchOf(capsuleDir, ex.CurrentStep)
}

// lastDispatchOf answers the newest dispatch of step on the capsule's spawn
// journal: the conversation a resume of the step continues. 0 when none
// (or the journal is unreadable).
func lastDispatchOf(capsuleDir, step string) int64 {
	if capsuleDir == "" {
		return 0
	}
	entries, err := capsule.ReadSpawnJournal(capsuleDir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if e.StepID == step && int64(e.SpawnN) > n {
			n = int64(e.SpawnN)
		}
	}
	return n
}

// endedAwaitingOwner reports whether the step's newest conversation (its
// newest dispatch on the spawn journal) ended with its agent waiting for
// the owner, the owner's answer not yet in it: the dispatch handed off
// `blocked`, or it asked (`pf ask`) and the reply to that ask is among
// the undelivered messages. Such a conversation resumes with the owner's
// messages alone (relay.go). Derived from the capsule's records, so a
// daemon restart or the re-claim of a parked work item keeps it.
func endedAwaitingOwner(capsuleDir, step string, undelivered []capsule.AskLine) bool {
	n := lastDispatchOf(capsuleDir, step)
	if n == 0 || len(undelivered) == 0 {
		return false
	}
	if lines, err := stepcall.Read(capsuleDir); err == nil && stepcall.EndedWith(lines, step, n) == stepcall.StatusBlocked {
		return true
	}
	asks, err := capsule.ReadAsks(capsuleDir)
	if err != nil {
		return false
	}
	for _, a := range asks {
		if a.Kind != capsule.AskKindAsk || a.Step != step || a.Spawn != n {
			continue
		}
		for _, m := range undelivered {
			if m.Kind == capsule.AskKindReply && m.AskAt == a.At {
				return true
			}
		}
	}
	return false
}
