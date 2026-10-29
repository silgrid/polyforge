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
// persisted in the anchor): a model-class death owes one step — except a
// model_error (an error-ended turn, typically a gateway 5xx), which owes
// it only as the second consecutive model-class death on that candidate
// (ModelErrorRotationThreshold): the first is a same-candidate resume; an
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
// the candidate pointer from its Step.Candidate anchor (a model_error only
// at ModelErrorRotationThreshold, see modelClassRotates).
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

// ModelErrorRotationThreshold is the same-candidate model-class streak
// (neutral causes skipped, ModelClassStreak) at which a model_error
// rotates the model. An error-ended turn is most often a gateway 5xx — an
// outage of the upstream, not a fault of the model — so the first one on a
// candidate resumes the same conversation on the same candidate and only a
// second consecutive model-class death moves on. Every other model-class
// cause (a hang, an allowance/auth/model-name exit, a slow-model timeout)
// rotates at once. A constant, never a config knob.
const ModelErrorRotationThreshold = 2

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

// streakEra answers the start of the step's current streak era for a NEW
// death: the newest answer or the owner's last continue of the blocked
// step (Supervision.ResumedAt), whichever is later (zero = neither). An
// attempt that started before it belongs to an earlier era, so the
// failure decision's streaks — the EWR and model-class rotations, the
// retry pacing — and RetryOwed do not reach across a human's answer or
// continue. Model selection (CandidatePointer / pendingAdvance) reads the
// answer era alone: the advance a chain-end or no-chain death still owes
// is exactly what the owner's "edit the list, then pf resume" collects.
func streakEra(st *capsule.Step) time.Time {
	era := answerEraStart(st.Answers)
	if st.Supervision != nil {
		if t, err := time.Parse(time.RFC3339Nano, st.Supervision.ResumedAt); err == nil && t.After(era) {
			era = t
		}
	}
	return era
}

// beforeEra reports whether an attempt started before the streak era.
func beforeEra(a capsule.Attempt, era time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, a.StartedAt)
	return err == nil && t.Before(era)
}

// SameCandidateEwrStreak counts the step's trailing EWR deaths on one
// candidate within one streak era (attempts started at or after era,
// streakEra): neutral causes are skipped, every other attempt (an accepted
// result, a rewind, a model-class or other counted failure, a different
// candidate, an earlier era) ends the streak.
func SameCandidateEwrStreak(attempts []capsule.Attempt, candidate string, era time.Time) int {
	n := 0
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		if a.Cause != CauseInfraFailure {
			break
		}
		if neutralCause(a.FailureCause) {
			continue
		}
		if a.FailureCause != FailEndedWithoutResult || a.Candidate != candidate || beforeEra(a, era) {
			break
		}
		n++
	}
	return n
}

// ModelClassStreak counts the step's trailing model-class deaths on one
// candidate within one streak era (attempts started at or after era,
// streakEra: the newest answer or the owner's last continue): neutral
// causes are skipped, every other attempt (an accepted result, a rewind, a
// counted failure of another class, a different candidate, an earlier
// era) ends the streak. It is the model_error rotation's ledger: a
// crash-recovered driver reads the same streak as its predecessor.
func ModelClassStreak(attempts []capsule.Attempt, candidate string, era time.Time) int {
	n := 0
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		if a.Cause != CauseInfraFailure {
			break
		}
		if neutralCause(a.FailureCause) {
			continue
		}
		if !IsModelClass(a.FailureCause) || a.Candidate != candidate || beforeEra(a, era) {
			break
		}
		n++
	}
	return n
}

// modelClassRotates is the one rule for a model-class death that rotates
// the model, asked by the failure decision (streak including the dying
// attempt) and by the pointer (streak of the archived attempts): every
// model-class cause rotates at once except model_error, which rotates at
// ModelErrorRotationThreshold.
func modelClassRotates(cause string, streak int) bool {
	if cause == FailModelError {
		return streak >= ModelErrorRotationThreshold
	}
	return IsModelClass(cause)
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
func pendingAdvance(attempts []capsule.Attempt, era time.Time, anchor string) (int, string) {
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
		if modelClassRotates(a.FailureCause, ModelClassStreak(attempts[:j+1], a.Candidate, era)) {
			return 1, a.FailureCause
		}
	case a.FailureCause == FailEndedWithoutResult:
		if ewrRotates(SameCandidateEwrStreak(attempts[:j+1], a.Candidate, era)) {
			return 1, a.FailureCause
		}
	}
	return 0, ""
}

// CandidatePointer answers the 0-based index into candidates of the
// step's current effective model (anchor + pending, see the file header),
// recomputed from the persisted anchor (Step.Candidate), the archived
// attempts (their streaks within the era, streakEra) and the live list
// alone. An empty list answers -1: no switchable chain exists and the
// harness adapter's default model stays in effect.
func CandidatePointer(anchor string, attempts []capsule.Attempt, era time.Time, candidates []string) int {
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
	n, _ := pendingAdvance(attempts, era, anchor)
	return min(idx+n, len(candidates)-1)
}
