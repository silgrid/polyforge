// candidates.go: the v2 model chain as a PURE function. The candidate
// pointer is not a persisted field of its own: it is recomputed from the
// step's persisted Step.Candidate (the model of its current/next dispatch:
// written at step_start and at the failure that decides a switch), the
// archived attempts and the LIVE candidate list of the dispatch
// (polyforge#33: composed at every dispatch, never snapshotted), so a
// crash-recovered driver derives the same model as its predecessor without
// process memory.
//
// The advancement rule (anchor + pending): the anchor is the persisted
// Step.Candidate, located BY IDENTITY in the live list: found → its index,
// empty (the era reset after a rewind) or removed from the list → the list
// head. Pending is the one advancement the newest non-neutral attempt still
// owes — and it owes it only when that attempt ran the anchor model (an
// attempt of another model means the switch it implied is already
// persisted in the anchor): a model-class death owes one step; an
// ended_without_result (EWR) death owes one step once the step's EWR
// streak on that candidate reaches EwrRotationThreshold. The pointer is
// anchor+pending capped at the last candidate, so a chain end keeps its
// end model across blocked/resume, and an owner edit that appends a
// candidate lets the same formula switch to it. Host-class deaths block
// the step and never advance; every other cause is a same-model retry.
// The rule reads the death cause the driver itself assigned — never free
// text.
package driver

import (
	"slices"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// IsModelClass reports whether a failure cause is a model-class death:
// with an EWR streak at EwrRotationThreshold, the only causes that advance
// the candidate pointer from its Step.Candidate anchor.
func IsModelClass(cause string) bool {
	switch cause {
	case FailModelError, FailModelHung, FailStepTimeoutSlowModel, FailExitModel:
		return true
	}
	return false
}

// EwrRotationThreshold is the EWR streak (same candidate, neutral causes
// skipped) at which an ended_without_result death rotates the model like a
// model_error. A constant, never a config knob (polyforge#33).
const EwrRotationThreshold = 2

// neutralCause reports the infra causes that neither count toward nor
// break an EWR streak: the uncounted ones (throttles and audit-only
// interruptions).
func neutralCause(c string) bool {
	switch c {
	case FailRateLimited, FailUpstreamTransient, FailDriverRelaunch, FailDaemonRestart:
		return true
	}
	return false
}

// ewrRotates is the one rule for an EWR streak that rotates the model:
// the failure decision (streak including the dying attempt) and the
// pointer (streak of the archived attempts) both ask it.
func ewrRotates(streak int) bool { return streak >= EwrRotationThreshold }

// answerEraStart answers the newest answered_at among the step's answers
// (zero time = none or unparsable): an attempt that started before it
// belongs to an earlier answer era.
func answerEraStart(answers []capsule.ArchivedAnswer) time.Time {
	var latest time.Time
	for _, a := range answers {
		if t, err := time.Parse(time.RFC3339Nano, a.AnsweredAt); err == nil && t.After(latest) {
			latest = t
		}
	}
	return latest
}

// SameCandidateEwrStreak counts the step's trailing EWR deaths on one
// candidate within one answer era (attempts started at or after the newest
// answer's answered_at): neutral causes are skipped, every other attempt
// (an accepted result, a rewind, a model-class or other counted failure, a
// different candidate, an earlier answer era) ends the streak.
func SameCandidateEwrStreak(attempts []capsule.Attempt, candidate string, answers []capsule.ArchivedAnswer) int {
	era := answerEraStart(answers)
	n := 0
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		if a.Cause != CauseInfraFailure {
			break
		}
		if neutralCause(a.FailureCause) {
			continue
		}
		if a.FailureCause != FailEndedWithoutResult || a.Candidate != candidate {
			break
		}
		if t, err := time.Parse(time.RFC3339Nano, a.StartedAt); err == nil && t.Before(era) {
			break
		}
		n++
	}
	return n
}

// pendingAdvance answers the one advancement the step's newest
// non-neutral attempt still owes against the anchor (the persisted
// Step.Candidate): 0 or 1. Trailing neutral attempts (an audit-only
// daemon_restart/driver_relaunch audit entries, a throttle) are skipped.
// The advance is owed only while the anchor still is the model that
// attempt ran: a death that decided a switch persisted it into the anchor,
// so what stays owed is a chain-end or no-chain death followed by an owner
// list edit that adds a later candidate. An attempt that ran a model other than
// the anchor owes nothing: the switch it implied is already the anchor.
func pendingAdvance(attempts []capsule.Attempt, answers []capsule.ArchivedAnswer, anchor string) (int, string) {
	j := len(attempts) - 1
	for j >= 0 && attempts[j].Cause == CauseInfraFailure && neutralCause(attempts[j].FailureCause) {
		j--
	}
	if j < 0 {
		return 0, ""
	}
	a := attempts[j]
	if a.Cause != CauseInfraFailure || a.Candidate != anchor {
		return 0, ""
	}
	switch {
	case IsModelClass(a.FailureCause):
		return 1, a.FailureCause
	case a.FailureCause == FailEndedWithoutResult:
		if ewrRotates(SameCandidateEwrStreak(attempts[:j+1], a.Candidate, answers)) {
			return 1, a.FailureCause
		}
	}
	return 0, ""
}

// CandidatePointer answers the 0-based index into candidates of the
// step's current effective model (anchor + pending, see the file header),
// recomputed from the persisted anchor (Step.Candidate), the archived
// attempts and the live list alone. An empty list answers -1: no
// switchable chain exists and the harness adapter's default model stays in
// effect.
func CandidatePointer(anchor string, attempts []capsule.Attempt, answers []capsule.ArchivedAnswer, candidates []string) int {
	if len(candidates) == 0 {
		return -1
	}
	if anchor == "" {
		return 0
	}
	idx := slices.Index(candidates, anchor)
	if idx < 0 {
		return 0 // the model left the list: the new list head
	}
	n, _ := pendingAdvance(attempts, answers, anchor)
	return min(idx+n, len(candidates)-1)
}
